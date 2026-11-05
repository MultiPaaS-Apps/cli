package main

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

func TestRecordsUploadSendsTheHashAndPutsWithTheSignedHeaders(t *testing.T) {
	fake := projectFake(t)
	body := []byte("photo bytes")
	path := writeExport(t, t.TempDir(), "photo.png", string(body))
	sum := sha256.Sum256(body)

	got := fake.run(t, "records", "upload", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--field", "field-9", path, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{
		"entityId": "entity-1", "fieldId": "field-9", "filename": "photo.png", "contentType": "image/png",
		"sizeBytes": float64(len(body)), "sha256": hex.EncodeToString(sum[:]),
	}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
	received, ok := fake.uploads["file-1"]
	if !ok || string(received.body) != string(body) || received.contentLength != int64(len(body)) {
		t.Fatalf("upload %+v", received)
	}
	if received.header.Get("Content-Type") != "image/png" || received.header.Get("X-Amz-Server-Side-Encryption") != "aws:kms" ||
		received.header.Get("X-Amz-Checksum-Sha256") == "" {
		t.Fatalf("headers %v", received.header)
	}
	if received.header.Get("Authorization") != "" || received.header.Get("X-Multipaas-Scope") != "" {
		t.Fatalf("headers %v", received.header)
	}
	if compact(got.stdout) != `{"file":{"id":"file-1"}}` {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsUploadPrintsOnlyTheFileId(t *testing.T) {
	fake := projectFake(t)
	path := writeExport(t, t.TempDir(), "photo.png", "photo bytes")
	got := fake.run(t, "records", "upload", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--field", "field-9", path)
	if got.exit != exitOK || got.stdout != "file-1\n" {
		t.Fatalf("exit %d: %q", got.exit, got.stdout)
	}
}

func TestRecordsUploadSendsOctetStreamForAnUnknownExtension(t *testing.T) {
	fake := projectFake(t)
	path := writeExport(t, t.TempDir(), "notes", "plain words")
	got := fake.run(t, "records", "upload", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--field", "field-9", path, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	input := fake.mutations[0]["input"].(map[string]any)
	if input["contentType"] != "application/octet-stream" || fake.uploads["file-1"].header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("sent %v", input)
	}
}

func TestRecordsUploadReportsARefusedPut(t *testing.T) {
	fake := projectFake(t)
	fake.refuseUpload = true
	path := writeExport(t, t.TempDir(), "photo.png", "photo bytes")
	got := fake.run(t, "records", "upload", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--field", "field-9", path, "--json")
	assertFailure(t, got, exitRefused, "UPLOAD_REFUSED")
	if !strings.Contains(got.stdout, "HTTP 403") || !strings.Contains(got.stdout, "nothing was attached") {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsUploadNeedsExactlyOnePath(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "upload", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--field", "field-9", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "accepts 1 arg(s), received 0") {
		t.Fatalf("printed %s", got.stdout)
	}
	first := writeExport(t, t.TempDir(), "a.png", "a")
	second := writeExport(t, t.TempDir(), "b.png", "b")
	two := fake.run(t, "records", "upload", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--field", "field-9", first, second, "--json")
	assertFailure(t, two, exitRefused, "CLI_ERROR")
	if !strings.Contains(two.stdout, "accepts 1 arg(s), received 2") {
		t.Fatalf("printed %s", two.stdout)
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestRecordsGetAndListPrintEachFile(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{{
		"id": "record-1", "data": map[string]any{"field-9": "file-7"}, "createdAt": "x", "updatedAt": "y",
		"files": []map[string]any{{
			"fieldId": "field-9", "fileId": "file-7", "filename": "photo.png", "contentType": "image/png",
			"sizeBytes": 11, "url": "https://files.example/photo", "urlExpiresAt": "z",
		}},
	}}
	got := fake.run(t, "records", "get", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1")
	if got.exit != exitOK || !strings.Contains(got.stdout, "file field-9 photo.png (image/png, 11 bytes) https://files.example/photo\n") {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	listed := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1")
	if listed.exit != exitOK || !strings.Contains(listed.stdout, "RECORD") || !strings.Contains(listed.stdout, "https://files.example/photo") {
		t.Fatalf("exit %d: %s", listed.exit, listed.stdout)
	}
	asJSON := fake.run(t, "records", "get", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1", "--json")
	if !strings.Contains(compact(asJSON.stdout), `"files":[{"fieldId":"field-9","fileId":"file-7","filename":"photo.png","contentType":"image/png","sizeBytes":11,"url":"https://files.example/photo","urlExpiresAt":"z"}]`) {
		t.Fatalf("printed %s", asJSON.stdout)
	}
}
