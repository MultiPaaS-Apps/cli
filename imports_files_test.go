package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestImportCreateUploadsFilesFolder(t *testing.T) {
	fake := projectFake(t)
	seedImportEntities(fake)
	dir := t.TempDir()
	writeExport(t, dir, "sessions.jsonl", "{\"id\":\"s-1\",\"photo\":\"pies/apple.jpg\"}\n")
	apple, pear := []byte("apple-bytes"), []byte("pear-bytes")
	writeNested(t, dir, "files/pies/apple.jpg", apple)
	writeNested(t, dir, "files/pear.png", pear)
	if err := os.Symlink(filepath.Join(dir, "files", "pear.png"), filepath.Join(dir, "files", "link.png")); err != nil {
		t.Fatal(err)
	}

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	type sent struct {
		filename, kind, contentType string
		size                        float64
		sha                         string
	}
	var adds []sent
	for _, variables := range fake.mutations[1:] {
		input := variables["input"].(map[string]any)
		adds = append(adds, sent{input["filename"].(string), input["kind"].(string), input["contentType"].(string), input["sizeBytes"].(float64), input["sha256"].(string)})
		if _, has := input["entityId"]; has && input["kind"] == "files" {
			t.Fatalf("a files part names no entity: %v", input)
		}
	}
	appleSum, pearSum := sha256.Sum256(apple), sha256.Sum256(pear)
	want := []sent{
		{"sessions.jsonl", "records", "application/x-ndjson", float64(len("{\"id\":\"s-1\",\"photo\":\"pies/apple.jpg\"}\n")), adds[0].sha},
		{"pear.png", "files", "image/png", float64(len(pear)), hex.EncodeToString(pearSum[:])},
		{"pies/apple.jpg", "files", "image/jpeg", float64(len(apple)), hex.EncodeToString(appleSum[:])},
	}
	if !reflect.DeepEqual(adds, want) {
		t.Fatalf("added %v, want %v", adds, want)
	}
	if string(fake.uploads["file-3"].body) != string(apple) || string(fake.uploads["file-2"].body) != string(pear) || fake.uploads["file-3"].header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("uploads %+v", fake.uploads)
	}
}

func TestImportCreateNoFilesFolder(t *testing.T) {
	fake := projectFake(t)
	dir := t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if len(fake.mutations) != 2 || fake.mutations[1]["input"].(map[string]any)["kind"] != "tenants" {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestImportCreateDirRefusesAFileNameOver255BytesBeforeCreatingAnything(t *testing.T) {
	fake := projectFake(t)
	dir := t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")
	long := strings.Repeat("a", 200) + "/" + strings.Repeat("b", 100) + ".txt"
	writeNested(t, dir, "files/"+long, []byte("x"))

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "IMPORT_FILE_INVALID")
	if len(fake.mutations) != 0 || len(fake.imports) != 0 {
		t.Fatalf("an import was created: %v %v", fake.mutations, fake.imports)
	}
}

func TestImportAddFilesKind(t *testing.T) {
	fake := projectFake(t)
	seedImportEntities(fake)
	fake.seedImport("import-1", "draft")
	path := writeNested(t, t.TempDir(), "photo.jpg", []byte("photo-bytes"))

	got := fake.run(t, "import", "add", "--app", importApp, "--import", "import-1", "--kind", "files", "--file", path, "--name", "pies/apple.jpg", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	got = fake.run(t, "import", "add", "--app", importApp, "--import", "import-1", "--kind", "files", "--file", path, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	names := []any{}
	for _, variables := range fake.mutations {
		input := variables["input"].(map[string]any)
		names = append(names, input["filename"])
		if input["kind"] != "files" || input["contentType"] != "image/jpeg" {
			t.Fatalf("sent %v", input)
		}
	}
	if !reflect.DeepEqual(names, []any{"pies/apple.jpg", "photo.jpg"}) {
		t.Fatalf("named %v", names)
	}

	before := len(fake.mutations)
	got = fake.run(t, "import", "add", "--app", importApp, "--import", "import-1", "--kind", "files", "--entity", "entity-1", "--file", path, "--json")
	if got.exit != exitRefused || len(fake.mutations) != before || !strings.Contains(got.stdout, "--entity is for --kind records") {
		t.Fatalf("--entity with files should be refused locally, naming --entity: exit %d, %v, %s", got.exit, fake.mutations[before:], got.stdout)
	}
}

func writeNested(t *testing.T, dir, rel string, body []byte) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
