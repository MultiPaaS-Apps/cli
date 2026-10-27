package main

// The import commands load an application's data from export files: create an import, add one
// JSON-lines file per kind (and per entity for records), check it, then run it.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

var (
	importPollInterval = 2 * time.Second
	importWaitLimit    = 20 * time.Minute
	uploadTimeout      = 15 * time.Minute
)

const (
	importContentType  = "application/x-ndjson"
	maxImportFileBytes = 512 << 20
)

var reservedKinds = map[string]DataImportFileKind{
	"tenants":     DataImportFileKindTenants,
	"users":       DataImportFileKindUsers,
	"memberships": DataImportFileKindMemberships,
}

// plannedFile is one file to add to an import: records files name their entity.
type plannedFile struct {
	path     string
	kind     DataImportFileKind
	entityID string
}

func importCommands(command commandFunc) *cobra.Command {
	return group("import", "Load an application's data from export files",
		importCreate(command), importAdd(command), importCheck(command), importRun(command), importStatus(command), importDelete(command))
}

func importCreate(command commandFunc) *cobra.Command {
	var appID, dir string
	cmd := command("create", "Create an import, adding every .jsonl file in --dir", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.appScope(ctx, appID, "")
		if err != nil {
			return nil, nil, err
		}
		var planned []plannedFile
		if dir != "" {
			if planned, err = planDir(ctx, s, scope, dir); err != nil {
				return nil, nil, err
			}
		}
		var created DataImportParts
		err = s.call(ctx, scope, func(client graphql.Client) error {
			response, err := CreateDataImport(ctx, client, appID)
			if err == nil {
				created = response.CreateDataImport.DataImportParts
			}
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		added := []string{}
		for _, file := range planned {
			if _, err := upload(ctx, s, scope, created.Id, file); err != nil {
				return nil, nil, partiallyCreated(err, created.Id, added)
			}
			added = append(added, filepath.Base(file.path))
		}
		if len(planned) > 0 {
			importID := created.Id
			if created, err = readImport(ctx, s, scope, importID, false); err != nil {
				return nil, nil, partiallyCreated(err, importID, added)
			}
		}
		return shownImport(created)
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&dir, "dir", "", "a directory of .jsonl files: tenants, users, memberships, and one per entity named for it")
	return cmd
}

func importAdd(command commandFunc) *cobra.Command {
	var appID, importID, kind, entityID, path string
	cmd := command("add", "Add one export file to an import", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		if !slices.Contains([]string{"tenants", "users", "memberships", "records"}, kind) {
			return nil, nil, fmt.Errorf("--kind must be one of tenants, users, memberships, records")
		}
		scope, err := s.appScope(ctx, appID, "")
		if err != nil {
			return nil, nil, err
		}
		added, err := upload(ctx, s, scope, importID, plannedFile{path: path, kind: DataImportFileKind(kind), entityID: entityID})
		if err != nil {
			return nil, nil, err
		}
		return map[string]DataImportFileParts{"importFile": added}, func(out io.Writer) {
			printFiles(out, []DataImportFileParts{added})
		}, nil
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&importID, "import", "", "the import's id")
	cmd.Flags().StringVar(&kind, "kind", "", "what the file holds: tenants, users, memberships or records")
	cmd.Flags().StringVar(&entityID, "entity", "", "the entity's id (from model show), for --kind records")
	cmd.Flags().StringVar(&path, "file", "", "the .jsonl file to upload")
	for _, name := range []string{"import", "kind", "file"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

func importCheck(command commandFunc) *cobra.Command {
	return importStep(command, "check", "Check every row of an import and report each problem by file and line",
		func(ctx context.Context, client graphql.Client, id string) (DataImportParts, error) {
			response, err := CheckDataImport(ctx, client, id)
			if err != nil {
				return DataImportParts{}, err
			}
			return response.CheckDataImport.DataImportParts, nil
		})
}

func importRun(command commandFunc) *cobra.Command {
	return importStep(command, "run", "Write a checked import's data, all or nothing",
		func(ctx context.Context, client graphql.Client, id string) (DataImportParts, error) {
			response, err := RunDataImport(ctx, client, id)
			if err != nil {
				return DataImportParts{}, err
			}
			return response.RunDataImport.DataImportParts, nil
		})
}

func importStatus(command commandFunc) *cobra.Command {
	var appID, importID string
	var includeDeleted bool
	cmd := command("status", "Show an import: its files, counts and every problem", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.appScope(ctx, appID, "")
		if err != nil {
			return nil, nil, err
		}
		current, err := readImport(ctx, s, scope, importID, includeDeleted)
		if err != nil {
			return nil, nil, err
		}
		if current, err = fullReport(ctx, current); err != nil {
			return nil, nil, err
		}
		return shownImport(current)
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&importID, "import", "", "the import's id")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "find the import even if it was deleted")
	_ = cmd.MarkFlagRequired("import")
	return cmd
}

func importDelete(command commandFunc) *cobra.Command {
	var appID, importID string
	cmd := command("delete", "Delete an import that is not checking or importing; its files and imported data stay", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.appScope(ctx, appID, "")
		if err != nil {
			return nil, nil, err
		}
		var deleted DataImportParts
		err = s.call(ctx, scope, func(client graphql.Client) error {
			response, err := DeleteDataImport(ctx, client, importID)
			if err == nil {
				deleted = response.DeleteDataImport.DataImportParts
			}
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		return shownImport(deleted)
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&importID, "import", "", "the import's id")
	_ = cmd.MarkFlagRequired("import")
	return cmd
}

// importStep is check or run: request it, wait for it to finish unless --wait=false, then show the import.
func importStep(command commandFunc, use, short string, request func(context.Context, graphql.Client, string) (DataImportParts, error)) *cobra.Command {
	var appID, importID string
	waiting := true
	cmd := command(use, short, func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.appScope(ctx, appID, "")
		if err != nil {
			return nil, nil, err
		}
		var current DataImportParts
		err = s.call(ctx, scope, func(client graphql.Client) (err error) {
			current, err = request(ctx, client, importID)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		if waiting {
			if current, err = wait(ctx, s, scope, current); err != nil {
				return nil, nil, err
			}
		}
		if current, err = fullReport(ctx, current); err != nil {
			return nil, nil, err
		}
		return shownImport(current)
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&importID, "import", "", "the import's id")
	cmd.Flags().BoolVar(&waiting, "wait", true, "wait until it finishes; --wait=false returns right after the request")
	_ = cmd.MarkFlagRequired("import")
	return cmd
}

// upload registers path with the import, then PUTs it to the presigned URL with exactly the
// headers the server signed. A refused PUT takes the file back off the import.
func upload(ctx context.Context, s *session, scope, importID string, file plannedFile) (DataImportFileParts, error) {
	size, digest, err := fileDigest(file.path)
	if err != nil {
		return DataImportFileParts{}, err
	}
	name := filepath.Base(file.path)
	input := map[string]any{
		"importId": importID, "kind": file.kind, "filename": name,
		"contentType": importContentType, "sizeBytes": size, "sha256": digest,
	}
	if file.entityID != "" {
		input["entityId"] = file.entityID
	}
	var added *AddDataImportFileResponse
	err = s.call(ctx, scope, func(client graphql.Client) (err error) {
		added, err = AddDataImportFile(ctx, client, input)
		return err
	})
	if err != nil {
		return DataImportFileParts{}, err
	}
	registered := added.AddDataImportFile.ImportFile.DataImportFileParts
	status, err := put(ctx, file.path, size, added.AddDataImportFile.UploadUrl, added.AddDataImportFile.UploadHeaders)
	if err == nil && status >= 200 && status < 300 {
		return registered, nil
	}
	reason := "no response"
	if status != 0 {
		reason = fmt.Sprintf("HTTP %d", status)
	}
	// RemoveDataImportFile takes the wrapped file's id, not the import's part id.
	removal := s.call(ctx, scope, func(client graphql.Client) error {
		_, err := RemoveDataImportFile(ctx, client, importID, registered.File.Id)
		return err
	})
	if removal != nil {
		return DataImportFileParts{}, &cliError{exit: exitRefused, Code: "UPLOAD_REFUSED", Message: fmt.Sprintf(
			"upload of %s refused (%s), and removing it failed: it is still listed on the import and cannot be added again. Run import status, then create a new import (import create --dir).", name, reason)}
	}
	return DataImportFileParts{}, &cliError{exit: exitRefused, Code: "UPLOAD_REFUSED", Message: fmt.Sprintf(
		"upload of %s refused (%s); it was removed from the import. Run import add again.", name, reason)}
}

// put sends path as the body of a PUT to target and returns the response status, or 0 with the error
// when there was no response. The error is not returned to the user: it names the presigned URL.
func put(ctx context.Context, path string, size int64, target string, headers []AddDataImportFileAddDataImportFileDataImportFileUploadUploadHeadersHeader) (int, error) {
	body, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer body.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, target, body)
	if err != nil {
		return 0, err
	}
	request.ContentLength = size
	for _, header := range headers {
		request.Header.Set(header.Name, header.Value)
	}
	response, err := transferClient().Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode, nil
}

// transferClient carries bodies too large for the session's timeout. It holds no bearer or scope, which a
// presigned URL does not take.
func transferClient() *http.Client {
	return &http.Client{Timeout: uploadTimeout, CheckRedirect: refuseRedirects}
}

// fileDigest streams path once for its size and lowercase-hex SHA-256.
func fileDigest(path string) (size int64, sha256Hex string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	if size, err = io.Copy(hash, file); err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

// wait polls the import until it leaves checking or importing.
func wait(ctx context.Context, s *session, scope string, current DataImportParts) (DataImportParts, error) {
	deadline := time.Now().Add(importWaitLimit)
	for current.Status == "checking" || current.Status == "importing" {
		if time.Now().After(deadline) {
			return current, &cliError{exit: exitRefused, Code: "IMPORT_STILL_RUNNING", Message: fmt.Sprintf(
				"import %s is still %s after %s. Run import status later.", current.Id, current.Status, importWaitLimit)}
		}
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-time.After(importPollInterval):
		}
		var err error
		if current, err = readImport(ctx, s, scope, current.Id, false); err != nil {
			return current, err
		}
	}
	return current, nil
}

// fullReport swaps the inline problems for the whole report when it was too big to keep inline.
func fullReport(ctx context.Context, current DataImportParts) (DataImportParts, error) {
	if current.ProblemReportUrl == nil || *current.ProblemReportUrl == "" {
		return current, nil
	}
	unavailable := func(reason string) error {
		return &cliError{exit: exitRefused, Code: "PROBLEM_REPORT_UNAVAILABLE", Message: "the full problem report could not be downloaded (" + reason + "). Run import status again."}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, *current.ProblemReportUrl, nil)
	if err != nil {
		return current, unavailable("bad link")
	}
	response, err := transferClient().Do(request)
	if err != nil {
		return current, unavailable("no response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return current, unavailable(fmt.Sprintf("HTTP %d", response.StatusCode))
	}
	problems := []DataImportPartsProblemsImportProblem{}
	lines := bufio.NewScanner(response.Body)
	lines.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for lines.Scan() {
		if len(strings.TrimSpace(lines.Text())) == 0 {
			continue
		}
		var problem DataImportPartsProblemsImportProblem
		if err := json.Unmarshal(lines.Bytes(), &problem); err != nil {
			return current, unavailable("unreadable report")
		}
		problems = append(problems, problem)
	}
	if lines.Err() != nil {
		return current, unavailable("unreadable report")
	}
	current.Problems = problems
	return current, nil
}

// planDir maps each .jsonl in dir to a kind, and records files to an entity, before anything is created.
func planDir(ctx context.Context, s *session, scope, dir string) ([]plannedFile, error) {
	listed, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, item := range listed {
		if !item.IsDir() && strings.HasSuffix(item.Name(), ".jsonl") {
			names = append(names, item.Name())
		}
	}
	if len(names) == 0 {
		return nil, &cliError{exit: exitRefused, Code: "IMPORT_DIR_EMPTY", Message: "no .jsonl files in " + dir}
	}
	if err := refuseUnusable(dir, names); err != nil {
		return nil, err
	}
	entities, err := listEntities(ctx, s, scope)
	if err != nil {
		return nil, err
	}
	planned, unmatched, ambiguous, clashes := []plannedFile{}, []string{}, []string{}, []string{}
	for _, name := range names {
		path := filepath.Join(dir, name)
		stem := strings.ToLower(strings.TrimSuffix(name, ".jsonl"))
		matches := entitiesNamed(entities, stem)
		if kind, reserved := reservedKinds[stem]; reserved {
			if len(matches) > 0 {
				clashes = append(clashes, name)
				continue
			}
			planned = append(planned, plannedFile{path: path, kind: kind})
			continue
		}
		switch len(matches) {
		case 0:
			unmatched = append(unmatched, name)
		case 1:
			planned = append(planned, plannedFile{path: path, kind: DataImportFileKindRecords, entityID: matches[0].Id})
		default:
			ambiguous = append(ambiguous, name)
		}
	}
	if len(clashes) > 0 {
		return nil, &cliError{exit: exitRefused, Code: "IMPORT_FILE_AMBIGUOUS", Message: strings.Join(clashes, ", ") +
			" is named for a reserved kind and for an entity. Add it with import add: --kind tenants, users or memberships for the reserved meaning, or --kind records --entity ENTITY_ID for the entity. Nothing was created."}
	}
	if len(unmatched) > 0 {
		return nil, &cliError{exit: exitRefused, Code: "IMPORT_FILE_UNMATCHED", Message: "no entity is named for " + strings.Join(unmatched, ", ") +
			". Name each file for an entity's name, type name or plural name (model show), or add it with import add. Nothing was created."}
	}
	if len(ambiguous) > 0 {
		return nil, &cliError{exit: exitRefused, Code: "IMPORT_FILE_AMBIGUOUS", Message: strings.Join(ambiguous, ", ") +
			" matches more than one entity. Rename the file, or add it with import add. Nothing was created."}
	}
	return planned, refuseDuplicates(planned)
}

// refuseUnusable fails, naming every file that cannot be opened or whose size the server would refuse.
func refuseUnusable(dir string, names []string) error {
	unusable := []string{}
	for _, name := range names {
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			unusable = append(unusable, name)
			continue
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil || info.Size() < 1 || info.Size() > maxImportFileBytes {
			unusable = append(unusable, name)
		}
	}
	if len(unusable) == 0 {
		return nil
	}
	return &cliError{exit: exitRefused, Code: "IMPORT_FILE_INVALID", Message: strings.Join(unusable, ", ") +
		" cannot be read, is empty, or is over 512 MiB. Nothing was created."}
}

// refuseDuplicates fails when two files map to the same kind, or to the same entity, as the server would on the second add.
func refuseDuplicates(planned []plannedFile) error {
	first := map[string]string{}
	for _, file := range planned {
		target := string(file.kind) + " " + file.entityID
		name := filepath.Base(file.path)
		if other, seen := first[target]; seen {
			what := string(file.kind)
			if file.entityID != "" {
				what = "entity " + file.entityID
			}
			return &cliError{exit: exitRefused, Code: "DUPLICATE_DATA_IMPORT_FILE", Message: fmt.Sprintf(
				"%s and %s both map to %s, and an import holds one file for each. Remove one. Nothing was created.", other, name, what)}
		}
		first[target] = name
	}
	return nil
}

func listEntities(ctx context.Context, s *session, scope string) ([]EntityParts, error) {
	return pages(func(page PageInput) ([]EntityParts, bool, error) {
		var listed *EntitiesResponse
		err := s.call(ctx, scope, func(client graphql.Client) (err error) {
			listed, err = Entities(ctx, client, page)
			return err
		})
		if err != nil {
			return nil, false, err
		}
		items := []EntityParts{}
		for _, item := range listed.Entities.Items {
			items = append(items, item.EntityParts)
		}
		return items, listed.Entities.PageInfo.HasMore, nil
	})
}

// entitiesNamed are the entities whose name, type name or plural name is stem, ignoring case.
func entitiesNamed(entities []EntityParts, stem string) []EntityParts {
	return slices.DeleteFunc(slices.Clone(entities), func(entity EntityParts) bool {
		return !slices.ContainsFunc([]string{entity.Name, entity.GraphqlTypeName, entity.GraphqlPluralName}, func(name string) bool {
			return strings.ToLower(name) == stem
		})
	})
}

// readImport is the import with id, or NOT_FOUND. A deleted import is found only with includeDeleted.
func readImport(ctx context.Context, s *session, scope, id string, includeDeleted bool) (DataImportParts, error) {
	var found *DataImportResponse
	err := s.call(ctx, scope, func(client graphql.Client) (err error) {
		found, err = DataImport(ctx, client, id, includeDeleted)
		return err
	})
	if err != nil {
		return DataImportParts{}, err
	}
	if found.DataImport == nil {
		return DataImportParts{}, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no import " + id + " that you can reach"}
	}
	return found.DataImport.DataImportParts, nil
}

// partiallyCreated says which import a failed create left behind and which files it holds.
func partiallyCreated(err error, importID string, added []string) error {
	failure := asCLIError(err)
	held := "no files"
	if len(added) > 0 {
		held = strings.Join(added, ", ")
	}
	return &cliError{exit: failure.exit, Code: failure.Code, FieldErrors: failure.FieldErrors, Message: fmt.Sprintf(
		"%s Import %s was created and holds %s.", failure.Message, importID, held)}
}

func shownImport(current DataImportParts) (any, func(io.Writer), error) {
	return map[string]DataImportParts{"import": current}, func(out io.Writer) { printImport(out, current) }, nil
}

func printImport(out io.Writer, current DataImportParts) {
	deleted := ""
	if current.DeletedAt != nil {
		deleted = " deleted " + *current.DeletedAt
	}
	fmt.Fprintf(out, "import %s %s%s\n", current.Id, current.Status, deleted)
	files := []DataImportFileParts{}
	for _, file := range current.Files {
		files = append(files, file.DataImportFileParts)
	}
	if len(files) > 0 {
		printFiles(out, files)
	}
	if len(current.Counts) > 0 {
		table(out, "KEY\tCOUNT", current.Counts, func(c DataImportPartsCountsDataImportCount) string {
			return fmt.Sprintf("%s\t%d", c.Key, c.Count)
		})
	}
	for _, problem := range current.Problems {
		line, field, sourceID := "-", "", ""
		if problem.Line != nil {
			line = fmt.Sprint(*problem.Line)
		}
		if problem.Field != nil {
			field = *problem.Field
		}
		if problem.SourceId != nil {
			sourceID = *problem.SourceId
		}
		fmt.Fprintf(out, "%s:%s  %s  %s  %s  %s\n", problem.File, line, problem.Code, field, sourceID, problem.Message)
	}
	if current.ProblemCount > 0 {
		fmt.Fprintf(out, "%d problems\n", current.ProblemCount)
	}
}

func printFiles(out io.Writer, files []DataImportFileParts) {
	table(out, "KIND\tENTITY\tFILE\tSIZE\tSTATUS\tFILE ID", files, func(f DataImportFileParts) string {
		entity := ""
		if f.Entity != nil {
			entity = f.Entity.Name
		}
		return fmt.Sprintf("%s\t%s\t%s\t%d\t%s\t%s", f.Kind, entity, f.File.Filename, f.File.SizeBytes, f.File.Status, f.File.Id)
	})
}
