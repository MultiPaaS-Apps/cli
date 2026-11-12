package main

// The import commands load an application's data from export files: create an import, add one
// JSON-lines file per kind (and per entity for records) and the record files they name, check it,
// then run it.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
	maxImportNameBytes = 255
	filesFolder        = "files"
)

var reservedKinds = map[string]DataImportFileKind{
	"tenants":     DataImportFileKindTenants,
	"users":       DataImportFileKindUsers,
	"memberships": DataImportFileKindMemberships,
}

// plannedFile is one file to add to an import: records files name their entity, and a files part
// carries the name a row's file field uses to refer to it.
type plannedFile struct {
	path     string
	kind     DataImportFileKind
	entityID string
	name     string
}

func importCommands(command commandFunc) *cobra.Command {
	return group("import", "Load an application's data from export files",
		importCreate(command), importAdd(command), importCheck(command), importRun(command), importStatus(command), importInvitations(command), importReissue(command), importDelete(command))
}

func importCreate(command commandFunc) *cobra.Command {
	var appID, envID, dir string
	cmd := command("create", "Create an import, adding every .jsonl file in --dir and every file under --dir/files, following symlinks", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.scopeIn(ctx, appID, envID)
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
		return shownImport(created, appID, envID)
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	cmd.Flags().StringVar(&dir, "dir", "", "a directory of .jsonl files: tenants, users, memberships, and one per entity named for it; every file under its files/ folder is added too, following symlinks, named by the path it is reached at under files/, for a row's file field to name")
	return cmd
}

func importAdd(command commandFunc) *cobra.Command {
	var appID, envID, importID, kind, entityID, path, name string
	cmd := command("add", "Add one export file, or one record file, to an import", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		if !slices.Contains([]string{"tenants", "users", "memberships", "records", "files"}, kind) {
			return nil, nil, fmt.Errorf("--kind must be one of tenants, users, memberships, records, files")
		}
		if kind == "files" && entityID != "" {
			return nil, nil, fmt.Errorf("--entity is for --kind records; a files part is named, not tied to an entity")
		}
		if kind != "files" && name != "" {
			return nil, nil, fmt.Errorf("--name is for --kind files")
		}
		if kind == "files" {
			if name == "" {
				name = filepath.Base(path)
			}
			if len(name) > maxImportNameBytes {
				return nil, nil, &cliError{exit: exitRefused, Code: "IMPORT_FILE_INVALID", Message: name + " is a name over 255 bytes."}
			}
		}
		scope, err := s.scopeIn(ctx, appID, envID)
		if err != nil {
			return nil, nil, err
		}
		added, err := upload(ctx, s, scope, importID, plannedFile{path: path, kind: DataImportFileKind(kind), entityID: entityID, name: name})
		if err != nil {
			return nil, nil, err
		}
		return map[string]DataImportFileParts{"importFile": added}, func(out io.Writer) {
			printFiles(out, []DataImportFileParts{added})
		}, nil
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	cmd.Flags().StringVar(&importID, "import", "", "the import's id")
	cmd.Flags().StringVar(&kind, "kind", "", "what the file holds: tenants, users, memberships, records, or files (a record file a row's file field names)")
	cmd.Flags().StringVar(&entityID, "entity", "", "the entity's id (from model show), for --kind records")
	cmd.Flags().StringVar(&name, "name", "", "the name a row's file field uses for the file, for --kind files; defaults to the file's base name")
	cmd.Flags().StringVar(&path, "file", "", "the file to upload: .jsonl, or any record file for --kind files")
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
	var appID, envID, importID string
	var includeDeleted bool
	cmd := command("status", "Show an import: its files, counts and every problem", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.scopeIn(ctx, appID, envID)
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
		return shownImport(current, appID, envID)
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	cmd.Flags().StringVar(&importID, "import", "", "the import's id")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "find the import even if it was deleted")
	_ = cmd.MarkFlagRequired("import")
	return cmd
}

const lapsedWindowHelp = `An imported membership is claimed at the person's next sign-in, within 7 days of the import run or of its last
re-issue. After that it lapses unclaimed.

Name the import with --import. Memberships from imports run before imports recorded their invitations carry no import,
so --import does not match them; name their tenant with --tenant.`

const reissueRulesHelp = `A re-issue gives every membership it re-issues another 7 days from now and sends no email. With --import alone, a
lapsed membership whose tenant or role is deleted, or whose role you do not hold, is skipped and stays lapsed. A deleted
import is refused as not found; name its tenant with --tenant instead. With --tenant, a tenant that is deleted, unknown
or outside the environment is refused as not found, and a membership whose role is deleted or whose role you do not hold
is skipped. With both flags, the tenant must be live and in the import's environment.`

func importInvitations(command commandFunc) *cobra.Command {
	var appID, envID, importID, tenantID string
	cmd := command("invitations", "List the lapsed import invitations of an import or a tenant", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.scopeIn(ctx, appID, envID)
		if err != nil {
			return nil, nil, err
		}
		where := map[string]any{"kind": "import", "status": "expired"}
		if importID != "" {
			where["dataImportId"] = importID
		}
		if tenantID != "" {
			where["appTenantId"] = tenantID
		}
		lapsed, err := pages(func(page PageInput) ([]ImportInvitationsInvitationsApplicationInvitationPageItemsApplicationInvitation, bool, error) {
			var listed *ImportInvitationsResponse
			err := s.call(ctx, scope, func(client graphql.Client) (err error) {
				listed, err = ImportInvitations(ctx, client, page, where)
				return err
			})
			if err != nil {
				return nil, false, err
			}
			return listed.Invitations.Items, listed.Invitations.PageInfo.HasMore, nil
		})
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"invitations": lapsed}, func(out io.Writer) { printLapsed(out, lapsed) }, nil
	})
	cmd.Long = "List the import invitations that lapsed unclaimed, for an import or a tenant. A tenant id that matches no tenant lists none.\n\n" + lapsedWindowHelp
	importTargetFlags(cmd, &appID, &envID, &importID, &tenantID)
	return cmd
}

func importReissue(command commandFunc) *cobra.Command {
	var appID, envID, importID, tenantID string
	cmd := command("reissue", "Re-issue the lapsed import invitations of an import or a tenant: re-issued ones are claimed at the user's next sign-in within 7 days", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.scopeIn(ctx, appID, envID)
		if err != nil {
			return nil, nil, err
		}
		input := map[string]any{}
		if importID != "" {
			input["dataImportId"] = importID
		}
		if tenantID != "" {
			input["appTenantId"] = tenantID
		}
		var result ReissueImportInvitationsReissueImportInvitationsReissueImportInvitationsResult
		err = s.call(ctx, scope, func(client graphql.Client) error {
			response, err := ReissueImportInvitations(ctx, client, input)
			if err == nil {
				result = response.ReissueImportInvitations
			}
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		return result, func(out io.Writer) { printReissued(out, result) }, nil
	})
	cmd.Long = "Re-issue the lapsed import invitations of an import or a tenant, so each re-issued one is claimed at the user's next sign-in within 7 days.\n\n" + lapsedWindowHelp + "\n\n" + reissueRulesHelp
	importTargetFlags(cmd, &appID, &envID, &importID, &tenantID)
	return cmd
}

// importTargetFlags are the flags that choose which import invitations a command reaches: at least one of
// --import and --tenant.
func importTargetFlags(cmd *cobra.Command, appID, envID, importID, tenantID *string) {
	appFlag(cmd, appID)
	envFlagFromProject(cmd, appID, envID, nil)
	cmd.Flags().StringVar(importID, "import", "", "the import's id")
	cmd.Flags().StringVar(tenantID, "tenant", "", "the tenant's id; reaches invitations from imports run before they recorded their import and, for reissue, those of a deleted import")
	cmd.MarkFlagsOneRequired("import", "tenant")
}

func printLapsed(out io.Writer, lapsed []ImportInvitationsInvitationsApplicationInvitationPageItemsApplicationInvitation) {
	table(out, "EMAIL\tTENANT\tROLE\tEXPIRED", lapsed, func(i ImportInvitationsInvitationsApplicationInvitationPageItemsApplicationInvitation) string {
		tenant := ""
		if i.AppTenant != nil {
			tenant = i.AppTenant.Name
		}
		roles := []string{}
		for _, role := range i.Roles {
			roles = append(roles, role.Name)
		}
		return fmt.Sprintf("%s\t%s\t%s\t%s", i.Email, tenant, strings.Join(roles, ","), i.ExpiresAt)
	})
	fmt.Fprintf(out, "%d lapsed\n", len(lapsed))
}

func printReissued(out io.Writer, result ReissueImportInvitationsReissueImportInvitationsReissueImportInvitationsResult) {
	fmt.Fprintf(out, "reissued %d, skipped %d\n", result.Reissued, result.Skipped)
	if result.Skipped > 0 {
		fmt.Fprintln(out, "skipped invitations have a deleted tenant or role, or a role you do not hold")
	}
}

func importDelete(command commandFunc) *cobra.Command {
	var appID, envID, importID string
	cmd := command("delete", "Delete an import that is not checking or importing; its files and imported data stay", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.scopeIn(ctx, appID, envID)
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
		return shownImport(deleted, appID, envID)
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	cmd.Flags().StringVar(&importID, "import", "", "the import's id")
	_ = cmd.MarkFlagRequired("import")
	return cmd
}

// importStep is check or run: request it, wait for it to finish unless --wait=false, then show the import.
func importStep(command commandFunc, use, short string, request func(context.Context, graphql.Client, string) (DataImportParts, error)) *cobra.Command {
	var appID, envID, importID string
	waiting := true
	cmd := command(use, short, func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.scopeIn(ctx, appID, envID)
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
		return shownImport(current, appID, envID)
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
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
	name, contentType := filepath.Base(file.path), importContentType
	if file.kind == DataImportFileKindFiles {
		name, contentType = file.name, fileContentType(file.path)
	}
	input := map[string]any{
		"importId": importID, "kind": file.kind, "filename": name,
		"contentType": contentType, "sizeBytes": size, "sha256": digest,
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
func put(ctx context.Context, path string, size int64, target string, headers []signedHeader) (int, error) {
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

// planDir maps each .jsonl in dir to a kind, records files to an entity, and adds every file
// under dir/files as a files part, following symlinks, before anything is created.
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
	recordFiles, err := listRecordFiles(dir)
	if err != nil {
		return nil, err
	}
	if err := refuseUnusable(dir, append(slices.Clone(names), recordFiles...)); err != nil {
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
	for _, rel := range recordFiles {
		planned = append(planned, plannedFile{path: filepath.Join(dir, rel), kind: DataImportFileKindFiles, name: strings.TrimPrefix(rel, filesFolder+"/")})
	}
	return planned, refuseDuplicates(planned)
}

// listRecordFiles are the files under dir/files, as slash-separated paths relative to dir, each named by
// the path it is reached at. Symlinks are followed, files/ itself included: a link to a file is that file,
// and a link to a folder is walked. A missing files folder holds none. A link that points at nothing, into a
// chain of links that loops, or at a folder it sits inside, is refused, naming every such link, before
// anything is created. Any other failure to read a path is returned as it is.
func listRecordFiles(dir string) ([]string, error) {
	walk := &recordFileWalk{dir: dir, found: []string{}}
	if err := walk.visit(filesFolder, nil); err != nil {
		return nil, err
	}
	if len(walk.dangling) > 0 || len(walk.circular) > 0 {
		parts := []string{}
		if len(walk.dangling) > 0 {
			parts = append(parts, "Cannot follow "+strings.Join(walk.dangling, ", ")+": a link that points at nothing, or into a chain of links that loops. Point each one straight at the file or folder it stands for, or remove it if that no longer exists.")
		}
		if len(walk.circular) > 0 {
			parts = append(parts, "Cannot follow "+strings.Join(walk.circular, ", ")+": a link to a folder it sits inside would repeat that folder's files without end. Remove each one.")
		}
		parts = append(parts, "Nothing was created.")
		return nil, &cliError{exit: exitRefused, Code: "IMPORT_FILE_INVALID", Message: strings.Join(parts, " ")}
	}
	return walk.found, nil
}

// recordFileWalk collects the files under dir/files, the links that point at nothing or loop among themselves,
// and the links to a folder they sit inside.
type recordFileWalk struct {
	dir                       string
	found, dangling, circular []string
}

// visit adds the entry reached at rel, a slash path under dir, following links. above holds the real
// paths of the folders rel sits inside.
func (w *recordFileWalk) visit(rel string, above []string) error {
	path := filepath.Join(w.dir, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		if _, lerr := os.Lstat(path); lerr != nil {
			if rel == filesFolder && os.IsNotExist(lerr) {
				return nil
			}
			return lerr
		}
		if !os.IsNotExist(err) && !errors.Is(err, syscall.ELOOP) {
			return err
		}
		w.dangling = append(w.dangling, rel)
		return nil
	}
	if info.Mode().IsRegular() {
		w.found = append(w.found, rel)
		return nil
	}
	if !info.IsDir() {
		return nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(above, func(folder string) bool { return within(folder, real) }) {
		w.circular = append(w.circular, rel)
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	above = append(slices.Clip(above), real)
	for _, entry := range entries {
		if err := w.visit(rel+"/"+entry.Name(), above); err != nil {
			return err
		}
	}
	return nil
}

// within reports whether path is folder or inside it.
func within(path, folder string) bool {
	rel, err := filepath.Rel(folder, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// refuseUnusable fails, naming every file that cannot be opened, whose size the server would refuse,
// or whose part name (the path under files/) is over 255 bytes.
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
		if err != nil || info.Size() < 1 || info.Size() > maxImportFileBytes || len(strings.TrimPrefix(name, filesFolder+"/")) > maxImportNameBytes {
			unusable = append(unusable, name)
		}
	}
	if len(unusable) == 0 {
		return nil
	}
	return &cliError{exit: exitRefused, Code: "IMPORT_FILE_INVALID", Message: strings.Join(unusable, ", ") +
		" cannot be read, is empty, is over 512 MiB, or has a name over 255 bytes. Nothing was created."}
}

// refuseDuplicates fails when two files map to the same kind, or to the same entity, as the server would on the second add.
func refuseDuplicates(planned []plannedFile) error {
	first := map[string]string{}
	for _, file := range planned {
		if file.kind == DataImportFileKindFiles {
			continue
		}
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

func shownImport(current DataImportParts, appID, envID string) (any, func(io.Writer), error) {
	return map[string]DataImportParts{"import": current}, func(out io.Writer) { printImport(out, current, appID, envID) }, nil
}

func printImport(out io.Writer, current DataImportParts, appID, envID string) {
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
	if inv := current.Invitations; inv.Pending+inv.Accepted+inv.Lapsed > 0 {
		fmt.Fprintf(out, "invitations: %d pending, %d accepted, %d lapsed\n", inv.Pending, inv.Accepted, inv.Lapsed)
		if inv.Lapsed > 0 && current.DeletedAt == nil {
			command := "multipaas import reissue"
			if appID != "" {
				command += " --app " + appID
			}
			if envID != "" {
				command += " --env " + envID
			}
			fmt.Fprintf(out, "run \"%s --import %s\" to re-issue the lapsed ones\n", command, current.Id)
		}
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
