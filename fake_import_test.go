package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
)

// fakeImport is a data import as the fake keeps it.
type fakeImport struct {
	status       string
	files        []map[string]any // DataImportFile rows
	problems     []map[string]any
	problemCount int
	reportURL    any
	counts       []map[string]any
}

// fakeUpload is what a presigned PUT received.
type fakeUpload struct {
	body          []byte
	header        http.Header
	contentLength int64
}

// seedImport is an import that already exists in the fake, in status.
func (f *fakeMultiPaaS) seedImport(id, status string) *fakeImport {
	imp := &fakeImport{status: status}
	f.imports[id] = imp
	return imp
}

// dataImport shares these steps with the server, in order: an application in scope, then the import
// (NOT_FOUND when unknown), then the change. A refused change is answered with refuseImport. Add and
// remove are not refused by status, which the server does.
func (f *fakeMultiPaaS) dataImport(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	if operation != "DataImport" {
		f.mutations = append(f.mutations, variables)
	}
	if appOf(parts) == "" {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	if operation == "CreateDataImport" {
		id := fmt.Sprintf("import-%d", len(f.imports)+1)
		f.imports[id] = &fakeImport{status: "draft"}
		writeData(w, map[string]any{"createDataImport": f.importView(id)})
		return
	}
	var id string
	switch operation {
	case "AddDataImportFile":
		id, _ = variables["input"].(map[string]any)["importId"].(string)
	case "DataImport":
		id, _ = variables["id"].(string)
	default:
		id, _ = variables["importId"].(string)
	}
	imp, known := f.imports[id]
	if !known {
		if operation == "DataImport" {
			writeData(w, map[string]any{"dataImport": nil})
			return
		}
		graphQLRefusal(w, "NOT_FOUND", "dataImport not found")
		return
	}
	if operation != "DataImport" && f.refuseImport[0] != "" {
		graphQLRefusal(w, f.refuseImport[0], f.refuseImport[1])
		return
	}
	switch operation {
	case "AddDataImportFile":
		f.addImportFile(w, imp, variables["input"].(map[string]any), appOf(parts))
	case "RemoveDataImportFile":
		fileID, _ := variables["fileId"].(string)
		i := slices.IndexFunc(imp.files, func(row map[string]any) bool { return row["file"].(map[string]any)["id"] == fileID })
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "dataImportFile not found")
			return
		}
		imp.files = slices.Delete(imp.files, i, i+1)
		writeData(w, map[string]any{"removeDataImportFile": map[string]any{"id": id}})
	case "CheckDataImport":
		imp.status = "checking"
		writeData(w, map[string]any{"checkDataImport": f.importView(id)})
	case "RunDataImport":
		imp.status = "importing"
		writeData(w, map[string]any{"runDataImport": f.importView(id)})
	case "DataImport":
		f.importReads++
		if queue := f.statusQueue[id]; len(queue) > 0 {
			imp.status, f.statusQueue[id] = queue[0], queue[1:]
		}
		writeData(w, map[string]any{"dataImport": f.importView(id)})
	}
}

// addImportFile registers a file under a part id and a distinct File id, and answers the presigned upload.
// The headers are the ones the server signs: the content type, the SHA-256 in base64, and the KMS header.
func (f *fakeMultiPaaS) addImportFile(w http.ResponseWriter, imp *fakeImport, input map[string]any, app string) {
	f.fileCount++
	fileID := fmt.Sprintf("file-%d", f.fileCount)
	var entity any
	if id, ok := input["entityId"].(string); ok {
		for _, row := range f.entities[app] {
			if row["id"] == id {
				entity = map[string]any{"id": id, "name": row["name"]}
			}
		}
	}
	added := map[string]any{
		"id": fmt.Sprintf("part-%d", f.fileCount), "kind": input["kind"], "entity": entity,
		"file": map[string]any{"id": fileID, "filename": input["filename"], "sizeBytes": input["sizeBytes"], "status": "pending"},
	}
	imp.files = append(imp.files, added)
	sum, _ := hex.DecodeString(input["sha256"].(string))
	writeData(w, map[string]any{"addDataImportFile": map[string]any{
		"importFile": added,
		"uploadUrl":  f.server.URL + "/upload/" + fileID,
		"uploadHeaders": []map[string]any{
			{"name": "content-type", "value": input["contentType"]},
			{"name": "x-amz-checksum-sha256", "value": base64.StdEncoding.EncodeToString(sum)},
			{"name": "x-amz-server-side-encryption", "value": "aws:kms"},
		},
	}})
}

func (f *fakeMultiPaaS) importView(id string) map[string]any {
	imp := f.imports[id]
	files := append([]map[string]any{}, imp.files...)
	var problems any
	if imp.problems != nil {
		problems = imp.problems
	}
	var counts any
	if imp.counts != nil {
		counts = imp.counts
	}
	return map[string]any{
		"id": id, "status": imp.status, "problemCount": imp.problemCount, "problemReportUrl": imp.reportURL,
		"problems": problems, "counts": counts, "files": files, "startedAt": nil, "finishedAt": nil,
	}
}

// receiveUpload is the presigned PUT. As S3 does, it refuses a body whose SHA-256 is not the one signed.
func (f *fakeMultiPaaS) receiveUpload(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuseUpload {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sum := sha256.Sum256(body)
	if r.Header.Get("x-amz-checksum-sha256") != base64.StdEncoding.EncodeToString(sum[:]) {
		http.Error(w, "BadDigest", http.StatusBadRequest)
		return
	}
	f.uploads[r.PathValue("id")] = fakeUpload{body: body, header: r.Header.Clone(), contentLength: r.ContentLength}
	w.WriteHeader(http.StatusOK)
}

// serveReport is the full problem report: one problem per JSON line.
func (f *fakeMultiPaaS) serveReport(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failReport {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	for _, line := range f.reportLines {
		_ = json.NewEncoder(w).Encode(line)
	}
}
