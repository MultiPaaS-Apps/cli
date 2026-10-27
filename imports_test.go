package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const importApp = "app-a-000"

func TestImportAddUploadsTheFileWithItsHashAndTheSignedHeaders(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "draft")
	body := []byte("{\"id\":\"u-9\",\"email\":\"pat@example.com\"}\n{\"id\":\"u-10\",\"email\":\"lee@example.com\"}\n")
	path := writeExport(t, t.TempDir(), "users.jsonl", string(body))
	sum := sha256.Sum256(body)

	got := fake.run(t, "import", "add", "--app", importApp, "--import", "import-1", "--kind", "users", "--file", path, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{
		"importId": "import-1", "kind": "users", "filename": "users.jsonl", "contentType": "application/x-ndjson",
		"sizeBytes": float64(len(body)), "sha256": hex.EncodeToString(sum[:]),
	}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	received, ok := fake.uploads["file-1"]
	if !ok || string(received.body) != string(body) || received.contentLength != int64(len(body)) {
		t.Fatalf("upload %+v", received)
	}
	signed := map[string]string{
		"Content-Type": "application/x-ndjson", "X-Amz-Server-Side-Encryption": "aws:kms",
		"X-Amz-Checksum-Sha256": received.header.Get("X-Amz-Checksum-Sha256"),
	}
	for name, value := range signed {
		if value == "" || received.header.Get(name) != value {
			t.Fatalf("header %s = %q, want %q (sent %v)", name, received.header.Get(name), value, received.header)
		}
	}
	if received.header.Get("X-Amz-Checksum-Sha256") == "" || received.header.Get("Authorization") != "" || received.header.Get("X-Multipaas-Scope") != "" {
		t.Fatalf("headers %v", received.header)
	}
	var printed struct{ ImportFile DataImportFileParts }
	decode(t, got.stdout, &printed)
	if printed.ImportFile.Id != "part-1" || printed.ImportFile.File.Id != "file-1" || printed.ImportFile.Kind != DataImportFileKindUsers {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestImportAddRemovesARefusedUploadByItsFileID(t *testing.T) {
	fake := projectFake(t)
	imp := fake.seedImport("import-1", "draft")
	fake.refuseUpload = true
	path := writeExport(t, t.TempDir(), "users.jsonl", "{\"id\":\"u-9\",\"email\":\"pat@example.com\"}\n")

	got := fake.run(t, "import", "add", "--app", importApp, "--import", "import-1", "--kind", "users", "--file", path, "--json")
	assertFailure(t, got, exitRefused, "UPLOAD_REFUSED")
	if len(fake.mutations) != 2 || !reflect.DeepEqual(fake.mutations[1], map[string]any{"importId": "import-1", "fileId": "file-1"}) {
		t.Fatalf("sent %v", fake.mutations)
	}
	if len(imp.files) != 0 {
		t.Fatalf("the refused file is still listed: %v", imp.files)
	}
}

func TestImportAddSendsNoEntityForANonRecordsKind(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "draft")
	path := writeExport(t, t.TempDir(), "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")

	got := fake.run(t, "import", "add", "--app", importApp, "--import", "import-1", "--kind", "tenants", "--file", path, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if input := fake.mutations[0]["input"].(map[string]any); !reflect.DeepEqual(input["kind"], "tenants") || slices.Contains(mapKeys(input), "entityId") {
		t.Fatalf("sent %v", input)
	}
	var printed struct {
		ImportFile struct{ Entity *DataImportFilePartsEntity }
	}
	decode(t, got.stdout, &printed)
	if printed.ImportFile.Entity != nil {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestImportAddSendsTheEntityForRecords(t *testing.T) {
	fake := projectFake(t)
	seedImportEntities(fake)
	fake.seedImport("import-1", "draft")
	path := writeExport(t, t.TempDir(), "sessions.jsonl", "{\"id\":\"s-301\",\"tenant\":\"acct-17\",\"fields\":{}}\n")

	got := fake.run(t, "import", "add", "--app", importApp, "--import", "import-1", "--kind", "records", "--entity", "entity-1", "--file", path, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	input := fake.mutations[0]["input"].(map[string]any)
	if input["kind"] != "records" || input["entityId"] != "entity-1" {
		t.Fatalf("sent %v", input)
	}
	var printed struct{ ImportFile DataImportFileParts }
	decode(t, got.stdout, &printed)
	if printed.ImportFile.Entity == nil || printed.ImportFile.Entity.Name != "Session" {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestImportAddRefusesAnUnknownKind(t *testing.T) {
	fake := projectFake(t)
	path := writeExport(t, t.TempDir(), "x.jsonl", "{}\n")
	got := fake.run(t, "import", "add", "--app", importApp, "--import", "import-1", "--kind", "widgets", "--file", path, "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestImportCreateDirMapsReservedNamesAndEntitiesByName(t *testing.T) {
	fake := projectFake(t)
	seedImportEntities(fake)
	dir := t.TempDir()
	for _, name := range []string{"tenants.jsonl", "users.jsonl", "memberships.jsonl", "Sessions.jsonl", "notes.txt"} {
		writeExport(t, dir, name, "{\"id\":\"x\"}\n")
	}

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if len(fake.mutations) != 5 || !reflect.DeepEqual(fake.mutations[0], map[string]any{"applicationId": importApp}) {
		t.Fatalf("sent %v", fake.mutations)
	}
	type sent struct{ filename, kind, entityID string }
	var adds []sent
	for _, variables := range fake.mutations[1:] {
		input := variables["input"].(map[string]any)
		entityID, _ := input["entityId"].(string)
		adds = append(adds, sent{input["filename"].(string), input["kind"].(string), entityID})
	}
	wantAdds := []sent{
		{"Sessions.jsonl", "records", "entity-1"}, {"memberships.jsonl", "memberships", ""},
		{"tenants.jsonl", "tenants", ""}, {"users.jsonl", "users", ""},
	}
	if !reflect.DeepEqual(adds, wantAdds) {
		t.Fatalf("added %v, want %v", adds, wantAdds)
	}
	var printed struct{ Import DataImportParts }
	decode(t, got.stdout, &printed)
	if printed.Import.Id != "import-1" || len(printed.Import.Files) != 4 {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestImportCreateDirRefusesAnUnmatchedFileBeforeCreatingAnything(t *testing.T) {
	fake := projectFake(t)
	seedImportEntities(fake)
	dir := t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")
	writeExport(t, dir, "widgets.jsonl", "{\"id\":\"w-1\"}\n")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "IMPORT_FILE_UNMATCHED")
	if !strings.Contains(got.stdout, "widgets.jsonl") || strings.Contains(got.stdout, "tenants.jsonl") {
		t.Fatalf("the message should name only widgets.jsonl: %s", got.stdout)
	}
	if len(fake.mutations) != 0 || len(fake.imports) != 0 {
		t.Fatalf("an import was created: %v %v", fake.mutations, fake.imports)
	}
}

func TestImportCreateDirRefusesANameMatchingTwoEntities(t *testing.T) {
	fake := projectFake(t)
	fake.entities[importApp] = []map[string]any{
		{"id": "entity-1", "name": "Item", "graphqlTypeName": "Item", "graphqlPluralName": "items", "managed": false},
		{"id": "entity-2", "name": "Items", "graphqlTypeName": "ItemSet", "graphqlPluralName": "itemSets", "managed": false},
	}
	dir := t.TempDir()
	writeExport(t, dir, "items.jsonl", "{\"id\":\"i-1\"}\n")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "IMPORT_FILE_AMBIGUOUS")
	if !strings.Contains(got.stdout, "items.jsonl") || len(fake.imports) != 0 {
		t.Fatalf("%s %v", got.stdout, fake.imports)
	}
}

func TestImportCreateDirRefusesTwoFilesForOneEntityBeforeCreatingAnything(t *testing.T) {
	fake := projectFake(t)
	seedImportEntities(fake)
	dir := t.TempDir()
	writeExport(t, dir, "Session.jsonl", "{\"id\":\"s-1\"}\n")
	writeExport(t, dir, "Sessions.jsonl", "{\"id\":\"s-2\"}\n")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "DUPLICATE_DATA_IMPORT_FILE")
	if !strings.Contains(got.stdout, "Session.jsonl") || !strings.Contains(got.stdout, "Sessions.jsonl") || !strings.Contains(got.stdout, "entity-1") {
		t.Fatalf("the message should name both files and the entity: %s", got.stdout)
	}
	if len(fake.mutations) != 0 || len(fake.imports) != 0 {
		t.Fatalf("an import was created: %v %v", fake.mutations, fake.imports)
	}
}

func TestImportCreateDirRefusesAReservedNameThatIsAlsoAnEntityBeforeCreatingAnything(t *testing.T) {
	fake := projectFake(t)
	fake.entities[importApp] = []map[string]any{
		{"id": "entity-1", "name": "User", "graphqlTypeName": "User", "graphqlPluralName": "users", "managed": false},
	}
	dir := t.TempDir()
	writeExport(t, dir, "users.jsonl", "{\"id\":\"u-9\",\"email\":\"pat@example.com\"}\n")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "IMPORT_FILE_AMBIGUOUS")
	if !strings.Contains(got.stdout, "users.jsonl") || !strings.Contains(got.stdout, "import add") {
		t.Fatalf("the message should name the file and import add: %s", got.stdout)
	}
	if len(fake.mutations) != 0 || len(fake.imports) != 0 {
		t.Fatalf("an import was created: %v %v", fake.mutations, fake.imports)
	}
}

func TestImportCreateDirRefusesAnEmptyFileBeforeCreatingAnything(t *testing.T) {
	fake := projectFake(t)
	dir := t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")
	writeExport(t, dir, "memberships.jsonl", "")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "IMPORT_FILE_INVALID")
	if !strings.Contains(got.stdout, "memberships.jsonl") || strings.Contains(got.stdout, "tenants.jsonl") {
		t.Fatalf("the message should name only memberships.jsonl: %s", got.stdout)
	}
	if len(fake.mutations) != 0 || len(fake.imports) != 0 {
		t.Fatalf("an import was created: %v %v", fake.mutations, fake.imports)
	}
}

func TestImportCreateDirRefusesADirWithNoJSONLines(t *testing.T) {
	fake := projectFake(t)
	dir := t.TempDir()
	writeExport(t, dir, "notes.txt", "x")

	assertFailure(t, fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json"), exitRefused, "IMPORT_DIR_EMPTY")
	if len(fake.imports) != 0 {
		t.Fatalf("an import was created: %v", fake.imports)
	}
}

func TestImportCreateWithoutADirMakesAnEmptyImport(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "import", "create", "--app", importApp, "--json")
	var printed struct{ Import DataImportParts }
	decode(t, got.stdout, &printed)
	if got.exit != exitOK || printed.Import.Id != "import-1" || printed.Import.Status != "draft" || len(printed.Import.Files) != 0 {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestImportCreateDirNamesTheImportWhenAnAddFails(t *testing.T) {
	fake := projectFake(t)
	fake.refuseUpload = true
	dir := t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "UPLOAD_REFUSED")
	if !strings.Contains(got.stdout, "import-1") {
		t.Fatalf("the message should name the import: %s", got.stdout)
	}
}

func TestImportCreateDirNamesTheImportWhenTheLastReadFails(t *testing.T) {
	fake := projectFake(t)
	fake.refuseRead = [2]string{"UNAVAILABLE", "the import could not be read"}
	dir := t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")
	writeExport(t, dir, "memberships.jsonl", "{\"user\":\"u-9\",\"tenant\":\"acct-17\",\"role\":\"Editor\"}\n")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "UNAVAILABLE")
	for _, want := range []string{"import-1", "tenants.jsonl", "memberships.jsonl", "could not be read"} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("the message should contain %q: %s", want, got.stdout)
		}
	}
}

func TestImportCreateDirKeepsTheFieldErrorsWhenTheLastReadFails(t *testing.T) {
	fake := projectFake(t)
	fake.refuseRead = [2]string{"VALIDATION", "the import is invalid"}
	fake.refuseReadFields = []map[string]any{{"field": "id", "rule": "format", "message": "id is malformed"}}
	dir := t.TempDir()
	writeExport(t, dir, "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")

	got := fake.run(t, "import", "create", "--app", importApp, "--dir", dir, "--json")
	assertFailure(t, got, exitRefused, "VALIDATION")
	var failure struct{ Error cliError }
	decode(t, got.stdout, &failure)
	if want := []fieldError{{Field: "id", Rule: "format", Message: "id is malformed"}}; !reflect.DeepEqual(failure.Error.FieldErrors, want) {
		t.Fatalf("fieldErrors %v, want %v", failure.Error.FieldErrors, want)
	}
}

func TestImportCheckWaitsUntilTheCheckFinishes(t *testing.T) {
	fake := projectFake(t)
	fastPolling(t)
	fake.seedImport("import-1", "draft")
	fake.statusQueue["import-1"] = []string{"checking", "checking", "check-failed"}

	got := fake.run(t, "import", "check", "--app", importApp, "--import", "import-1", "--json")
	var printed struct{ Import DataImportParts }
	decode(t, got.stdout, &printed)
	if got.exit != exitOK || printed.Import.Status != "check-failed" || fake.importReads != 3 {
		t.Fatalf("exit %d, %d reads: %s", got.exit, fake.importReads, got.stdout)
	}
}

func TestImportCheckWithoutWaitReturnsAtOnce(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "draft")

	got := fake.run(t, "import", "check", "--app", importApp, "--import", "import-1", "--wait=false", "--json")
	var printed struct{ Import DataImportParts }
	decode(t, got.stdout, &printed)
	if got.exit != exitOK || printed.Import.Status != "checking" || fake.importReads != 0 {
		t.Fatalf("exit %d, %d reads: %s", got.exit, fake.importReads, got.stdout)
	}
}

func TestImportCheckGivesUpAfterTheWaitLimit(t *testing.T) {
	fake := projectFake(t)
	fastPolling(t)
	importWaitLimit = 5 * time.Millisecond
	fake.seedImport("import-1", "draft")
	fake.statusQueue["import-1"] = slices.Repeat([]string{"checking"}, 1000)

	got := fake.run(t, "import", "check", "--app", importApp, "--import", "import-1", "--json")
	assertFailure(t, got, exitRefused, "IMPORT_STILL_RUNNING")
	if !strings.Contains(got.stdout, "import-1") {
		t.Fatalf("the message should name the import: %s", got.stdout)
	}
}

func TestImportCheckPrintsEveryProblemByFileAndLine(t *testing.T) {
	fake := projectFake(t)
	fastPolling(t)
	imp := fake.seedImport("import-1", "draft")
	imp.problemCount = 2
	imp.problems = []map[string]any{
		{"file": "users.jsonl", "line": 3, "sourceId": "u-9", "field": "email", "code": "invalid-email", "message": "email is not valid"},
		{"file": "", "line": nil, "sourceId": nil, "field": nil, "code": "check-error", "message": "the check failed"},
	}

	fake.statusQueue["import-1"] = []string{"check-failed"}
	human := fake.run(t, "import", "check", "--app", importApp, "--import", "import-1")
	if human.exit != exitOK {
		t.Fatalf("exit %d: %s", human.exit, human.stderr)
	}
	for _, line := range []string{"users.jsonl:3  invalid-email  email  u-9  email is not valid", ":-  check-error      the check failed", "2 problems"} {
		if !strings.Contains(human.stdout, line) {
			t.Fatalf("missing %q in:\n%s", line, human.stdout)
		}
	}

	fake.statusQueue["import-1"] = []string{"check-failed"}
	got := fake.run(t, "import", "check", "--app", importApp, "--import", "import-1", "--json")
	var printed struct{ Import DataImportParts }
	decode(t, got.stdout, &printed)
	if len(printed.Import.Problems) != 2 || printed.Import.Problems[0].Code != "invalid-email" || *printed.Import.Problems[0].Line != 3 ||
		printed.Import.Problems[1].Line != nil {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestImportStatusFetchesTheFullReportWhenItWasTooBigToKeepInline(t *testing.T) {
	fake := projectFake(t)
	imp := fake.seedImport("import-1", "check-failed")
	problem := func(line int, code string) map[string]any {
		return map[string]any{"file": "users.jsonl", "line": line, "sourceId": nil, "field": nil, "code": code, "message": "m"}
	}
	imp.problemCount = 3
	imp.problems = []map[string]any{problem(1, "invalid-json"), problem(2, "missing-id")}
	imp.reportURL = fake.server.URL + "/report"
	fake.reportLines = []map[string]any{problem(1, "invalid-json"), problem(2, "missing-id"), problem(3, "invalid-email")}

	got := fake.run(t, "import", "status", "--app", importApp, "--import", "import-1", "--json")
	var printed struct{ Import DataImportParts }
	decode(t, got.stdout, &printed)
	var codes []string
	for _, p := range printed.Import.Problems {
		codes = append(codes, p.Code)
	}
	if got.exit != exitOK || !reflect.DeepEqual(codes, []string{"invalid-json", "missing-id", "invalid-email"}) {
		t.Fatalf("exit %d, problems %v: %s", got.exit, codes, got.stdout)
	}

}

func TestImportStatusFailsWhenTheFullReportCannotBeDownloaded(t *testing.T) {
	fake := projectFake(t)
	imp := fake.seedImport("import-1", "check-failed")
	imp.problemCount = 3
	imp.problems = []map[string]any{{"file": "users.jsonl", "line": 1, "sourceId": nil, "field": nil, "code": "invalid-json", "message": "m"}}
	imp.reportURL = fake.server.URL + "/report"
	fake.failReport = true
	failed := fake.run(t, "import", "status", "--app", importApp, "--import", "import-1", "--json")
	assertFailure(t, failed, exitRefused, "PROBLEM_REPORT_UNAVAILABLE")
	if strings.Contains(failed.stdout, "/report") {
		t.Fatalf("the message names the report link: %s", failed.stdout)
	}
}

func TestImportRunPrintsTheCounts(t *testing.T) {
	fake := projectFake(t)
	fastPolling(t)
	imp := fake.seedImport("import-1", "checked")
	imp.counts = []map[string]any{{"key": "tenants", "count": 2}, {"key": "records:entity-1", "count": 5}}
	fake.statusQueue["import-1"] = []string{"importing", "imported"}

	got := fake.run(t, "import", "run", "--app", importApp, "--import", "import-1")
	if got.exit != exitOK || !strings.HasPrefix(got.stdout, "import import-1 imported\n") ||
		!regexp.MustCompile(`(?m)^tenants\s+2$`).MatchString(got.stdout) || !regexp.MustCompile(`(?m)^records:entity-1\s+5$`).MatchString(got.stdout) {
		t.Fatalf("exit %d: %s%s", got.exit, got.stdout, got.stderr)
	}
}

func TestImportRunKeepsTheServersRefusal(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "draft")
	fake.refuseImport = [2]string{"DATA_IMPORT_STATE", "This import can't do that in its current status."}

	got := fake.run(t, "import", "run", "--app", importApp, "--import", "import-1", "--json")
	assertFailure(t, got, exitRefused, "DATA_IMPORT_STATE")
}

func TestImportDeleteMarksTheImportDeleted(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "check-failed")

	got := fake.run(t, "import", "delete", "--app", importApp, "--import", "import-1", "--json")
	var printed struct{ Import DataImportParts }
	decode(t, got.stdout, &printed)
	if got.exit != exitOK || printed.Import.Id != "import-1" || printed.Import.DeletedAt == nil {
		t.Fatalf("exit %d: %s%s", got.exit, got.stdout, got.stderr)
	}
	if fake.imports["import-1"].deletedAt == nil {
		t.Fatal("import-1 was not deleted")
	}
}

func TestImportDeletePrintsTheImportMarkedDeleted(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "imported")

	got := fake.run(t, "import", "delete", "--app", importApp, "--import", "import-1")
	if got.exit != exitOK || !strings.HasPrefix(got.stdout, "import import-1 imported deleted 2026-10-26T12:00:00.000Z\n") {
		t.Fatalf("exit %d: %s%s", got.exit, got.stdout, got.stderr)
	}
}

func TestImportDeleteKeepsTheServersRefusal(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "checking")
	fake.refuseImport = [2]string{"DATA_IMPORT_STATE", "This import can't do that in its current status."}

	assertFailure(t, fake.run(t, "import", "delete", "--app", importApp, "--import", "import-1", "--json"), exitRefused, "DATA_IMPORT_STATE")
	if fake.imports["import-1"].deletedAt != nil {
		t.Fatal("import-1 was deleted anyway")
	}
}

func TestImportDeleteOfAnUnknownOrDeletedImport(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "draft").deletedAt = "2026-10-25T00:00:00.000Z"
	for _, id := range []string{"import-1", "import-9"} {
		assertFailure(t, fake.run(t, "import", "delete", "--app", importApp, "--import", id, "--json"), exitRefused, "NOT_FOUND")
	}
}

func TestImportStatusOfAnUnknownImport(t *testing.T) {
	fake := projectFake(t)
	assertFailure(t, fake.run(t, "import", "status", "--app", importApp, "--import", "import-9", "--json"), exitRefused, "NOT_FOUND")
}

func TestImportStatusIncludeDeletedShowsADeletedImportMarkedDeleted(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "check-failed").deletedAt = "2026-10-25T09:30:00.000Z"

	got := fake.run(t, "import", "status", "--app", importApp, "--import", "import-1", "--include-deleted", "--json")
	var printed struct{ Import DataImportParts }
	decode(t, got.stdout, &printed)
	if got.exit != exitOK || printed.Import.DeletedAt == nil || *printed.Import.DeletedAt != "2026-10-25T09:30:00.000Z" {
		t.Fatalf("exit %d: %s%s", got.exit, got.stdout, got.stderr)
	}
	text := fake.run(t, "import", "status", "--app", importApp, "--import", "import-1", "--include-deleted")
	if text.exit != exitOK || !strings.HasPrefix(text.stdout, "import import-1 check-failed deleted 2026-10-25T09:30:00.000Z\n") {
		t.Fatalf("exit %d: %s%s", text.exit, text.stdout, text.stderr)
	}
}

func TestImportStatusWithoutIncludeDeletedDoesNotFindADeletedImport(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-1", "check-failed").deletedAt = "2026-10-25T09:30:00.000Z"
	assertFailure(t, fake.run(t, "import", "status", "--app", importApp, "--import", "import-1", "--json"), exitRefused, "NOT_FOUND")
}

func TestImportCommandsUseTheApplicationScope(t *testing.T) {
	fake := projectFake(t)
	fake.seedImport("import-9", "draft")
	path := writeExport(t, t.TempDir(), "tenants.jsonl", "{\"id\":\"acct-17\",\"name\":\"Acme\"}\n")
	commands := [][]string{
		{"import", "create"},
		{"import", "add", "--import", "import-9", "--kind", "tenants", "--file", path},
		{"import", "check", "--import", "import-9", "--wait=false"},
		{"import", "run", "--import", "import-9", "--wait=false"},
		{"import", "status", "--import", "import-9"},
		{"import", "delete", "--import", "import-9"},
	}
	for _, args := range commands {
		got := fake.run(t, append(append([]string{}, args...), "--app", importApp, "--json")...)
		if got.exit != exitOK {
			t.Fatalf("%v: exit %d: %s", args, got.exit, got.stdout)
		}
		if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/"+importApp {
			t.Fatalf("%v: scope %q", args, last)
		}
	}
}

func fastPolling(t *testing.T) {
	interval, limit := importPollInterval, importWaitLimit
	importPollInterval = time.Millisecond
	t.Cleanup(func() { importPollInterval, importWaitLimit = interval, limit })
}

func seedImportEntities(fake *fakeMultiPaaS) {
	fake.entities[importApp] = []map[string]any{
		{"id": "entity-1", "name": "Session", "graphqlTypeName": "Session", "graphqlPluralName": "sessions", "managed": false},
		{"id": "entity-2", "name": "Note", "graphqlTypeName": "Note", "graphqlPluralName": "notes", "managed": false},
	}
}

func writeExport(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mapKeys(m map[string]any) []string {
	keys := []string{}
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
