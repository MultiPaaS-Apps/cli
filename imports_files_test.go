package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
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
		{"link.png", "files", "image/png", float64(len(pear)), hex.EncodeToString(pearSum[:])},
		{"pear.png", "files", "image/png", float64(len(pear)), hex.EncodeToString(pearSum[:])},
		{"pies/apple.jpg", "files", "image/jpeg", float64(len(apple)), hex.EncodeToString(appleSum[:])},
	}
	if !reflect.DeepEqual(adds, want) {
		t.Fatalf("added %v, want %v", adds, want)
	}
	if string(fake.uploads["file-4"].body) != string(apple) || string(fake.uploads["file-3"].body) != string(pear) || string(fake.uploads["file-2"].body) != string(pear) || fake.uploads["file-4"].header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("uploads %+v", fake.uploads)
	}
}

func TestImportCreateFollowsSymlinksUnderFilesFolder(t *testing.T) {
	fake := projectFake(t)
	seedImportEntities(fake)
	dir, elsewhere := t.TempDir(), t.TempDir()
	writeExport(t, dir, "sessions.jsonl", "{\"id\":\"s-1\",\"photo\":\"album/apple.jpg\"}\n")
	apple, pear := []byte("apple-bytes"), []byte("pear-bytes")
	writeNested(t, elsewhere, "photos/apple.jpg", apple)
	writeNested(t, elsewhere, "pear.png", pear)
	for link, target := range map[string]string{
		filepath.Join(elsewhere, "album"):    filepath.Join(elsewhere, "photos"),
		filepath.Join(elsewhere, "link.png"): filepath.Join(elsewhere, "pear.png"),
		filepath.Join(dir, "files"):          elsewhere,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	sums := map[string]string{}
	for _, variables := range fake.mutations[1:] {
		input := variables["input"].(map[string]any)
		if input["kind"] == "files" {
			sums[input["filename"].(string)] = input["sha256"].(string)
		}
	}
	appleSum, pearSum := sha256.Sum256(apple), sha256.Sum256(pear)
	want := map[string]string{
		"album/apple.jpg":  hex.EncodeToString(appleSum[:]),
		"link.png":         hex.EncodeToString(pearSum[:]),
		"pear.png":         hex.EncodeToString(pearSum[:]),
		"photos/apple.jpg": hex.EncodeToString(appleSum[:]),
	}
	if !reflect.DeepEqual(sums, want) {
		t.Fatalf("added files %v, want %v", sums, want)
	}
}

func TestImportCreateRefusesSymlinksThatCannotBeFollowed(t *testing.T) {
	fake := projectFake(t)
	dir := t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")
	writeNested(t, dir, "files/sub/kept.png", []byte("kept"))
	for link, target := range map[string]string{
		"files/gone.png": filepath.Join(dir, "missing.png"),
		"files/self":     filepath.Join(dir, "files"),
		"files/sub/up":   dir,
		"files/ping":     filepath.Join(dir, "files", "pong"),
		"files/pong":     filepath.Join(dir, "files", "ping"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "IMPORT_FILE_INVALID")
	var failure struct{ Error cliError }
	decode(t, got.stdout, &failure)
	for _, part := range []string{
		"Cannot follow files/gone.png, files/ping, files/pong: a link that points at nothing",
		"Cannot follow files/self, files/sub/up: a link to a folder it sits inside",
	} {
		if !strings.Contains(failure.Error.Message, part) {
			t.Fatalf("message %q does not contain %q", failure.Error.Message, part)
		}
	}
	if strings.Contains(failure.Error.Message, "kept.png") {
		t.Fatalf("message %q names a file that can be followed", failure.Error.Message)
	}
	if len(fake.mutations) != 0 || len(fake.imports) != 0 {
		t.Fatalf("an import was created: %v %v", fake.mutations, fake.imports)
	}
}

func TestImportCreateSurfacesAnUnreadableLinkTarget(t *testing.T) {
	fake := projectFake(t)
	dir, elsewhere := t.TempDir(), t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")
	writeNested(t, elsewhere, "locked/x.png", []byte("x"))
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "locked", "x.png"), filepath.Join(dir, "files", "x.png")); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(elsewhere, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if _, err := os.Stat(filepath.Join(dir, "files", "x.png")); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("premise: stat through the link must fail with permission denied, got %v", err)
	}

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	var failure struct{ Error cliError }
	decode(t, got.stdout, &failure)
	if !strings.Contains(failure.Error.Message, "permission denied") {
		t.Fatalf("message %q", failure.Error.Message)
	}
	if len(fake.mutations) != 0 || len(fake.imports) != 0 {
		t.Fatalf("an import was created: %v %v", fake.mutations, fake.imports)
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
