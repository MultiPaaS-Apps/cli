package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
)

// requestRecordFileUpload answers as the server does: a new file id and a PUT to the fake's upload route signed for
// the declared type, hash and key.
func (f *fakeMultiPaaS) requestRecordFileUpload(w http.ResponseWriter, variables map[string]any) {
	f.mutations = append(f.mutations, variables)
	input := variables["input"].(map[string]any)
	f.fileCount++
	fileID := fmt.Sprintf("file-%d", f.fileCount)
	sum, _ := hex.DecodeString(input["sha256"].(string))
	writeData(w, map[string]any{"requestRecordFileUpload": map[string]any{
		"fileId":    fileID,
		"url":       f.server.URL + "/upload/" + fileID,
		"expiresAt": "2026-10-11T00:15:00.000Z",
		"headers": []map[string]any{
			{"name": "content-type", "value": input["contentType"]},
			{"name": "x-amz-checksum-sha256", "value": base64.StdEncoding.EncodeToString(sum)},
			{"name": "x-amz-server-side-encryption", "value": "aws:kms"},
		},
	}})
}

// withFiles is a record as the server returns it: it always carries its files, none unless the test gave some.
func withFiles(record map[string]any) map[string]any {
	if _, ok := record["files"]; ok {
		return record
	}
	shown := maps.Clone(record)
	shown["files"] = []map[string]any{}
	return shown
}
