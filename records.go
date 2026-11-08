package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// recordTarget is the app-tenant, or with appWide the application itself, and the entity a records
// command acts on.
type recordTarget struct {
	appID, tenantID, envID, entityID string
	appWide                          bool
}

// recordCommands read and write an entity's records in one app-tenant, or the application's app-wide
// records, as the developer: test data for an app built on the application. A record's data is a JSON
// object keyed by field id.
func recordCommands(command commandFunc) *cobra.Command {
	return group("records", "Read and write an entity's records in an app-tenant, or the application's app-wide records",
		recordsList(command), recordGet(command), recordCreate(command), recordUpdate(command), recordRetire(command), recordUpload(command))
}

func recordsList(command commandFunc) *cobra.Command {
	var target recordTarget
	var parentField, parentRecord, where, orderBy string
	var count bool
	cmd := command("list", "List an entity's records, newest first unless --order-by says otherwise", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var reference *RecordReferenceInput
		if parentField != "" || parentRecord != "" {
			if parentField == "" || parentRecord == "" {
				return nil, nil, fmt.Errorf("--parent-field and --parent-record go together")
			}
			reference = &RecordReferenceInput{FieldId: parentField, RecordId: parentRecord}
		}
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return nil, nil, err
		}
		recordScope := RecordScopeTenant
		if target.appWide {
			recordScope = RecordScopeAppWide
		}
		whereJSON, err := whereArg(where)
		if err != nil {
			return nil, nil, err
		}
		order, err := orderByArg(orderBy)
		if err != nil {
			return nil, nil, err
		}
		if count {
			if order != nil {
				return nil, nil, fmt.Errorf("--count takes no --order-by")
			}
			var counted *RecordsCountResponse
			err := s.call(ctx, scope, func(client graphql.Client) (err error) {
				counted, err = RecordsCount(ctx, client, target.entityID, PageInput{Page: 0, PageSize: 1}, reference, recordScope, whereJSON)
				return err
			})
			if err != nil {
				return nil, nil, err
			}
			total := counted.Records.TotalCount
			return map[string]int{"count": total}, func(out io.Writer) { fmt.Fprintln(out, total) }, nil
		}
		list, err := pages(func(page PageInput) ([]RecordParts, bool, error) {
			var listed *RecordsResponse
			err := s.call(ctx, scope, func(client graphql.Client) (err error) {
				listed, err = Records(ctx, client, target.entityID, page, reference, recordScope, whereJSON, order)
				return err
			})
			if err != nil {
				return nil, false, err
			}
			found := []RecordParts{}
			for _, item := range listed.Records.Items {
				found = append(found, item.RecordParts)
			}
			return found, listed.Records.PageInfo.HasMore, nil
		})
		return list, func(out io.Writer) {
			table(out, "ID\tVERSION\tUPDATED\tDATA", list, func(r RecordParts) string {
				return r.Id + "\t" + r.Version + "\t" + r.UpdatedAt + "\t" + string(r.Data)
			})
			printRecordFiles(out, list)
		}, err
	})
	recordFlags(cmd, &target)
	previous := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if previous != nil {
			if err := previous(cmd, args); err != nil {
				return err
			}
		}
		switch {
		case cmd.Flags().Changed("where") && where == "":
			return fmt.Errorf(`--where is empty: give a JSON object of conditions, like '{"FIELD_ID": {"eq": "value"}}', or leave --where off`)
		case cmd.Flags().Changed("order-by") && orderBy == "":
			return fmt.Errorf("--order-by is empty: give createdAt, updatedAt or a field id, or leave --order-by off for newest first")
		}
		return nil
	}
	cmd.Flags().StringVar(&parentField, "parent-field", "", "keep the records whose parent reference field has this id (from model show), with --parent-record")
	cmd.Flags().StringVar(&parentRecord, "parent-record", "", "the parent record's id, with --parent-field")
	cmd.Flags().StringVar(&where, "where", "", `keep the records meeting every condition, as JSON keyed by field id or id, createdAt or updatedAt, like '{"FIELD_ID": {"lte": 5}}'; a related field takes {"some": {...}} or {"none": {...}} and a reference {"is": {...}}, keyed by the other entity's field ids, with {} for any record and never null`)
	cmd.Flags().StringVar(&orderBy, "order-by", "", "order by createdAt, updatedAt or a field id, ascending unless it ends :desc; a reference, related or file field, or an enum field whose enum no longer exists, is refused")
	cmd.Flags().BoolVar(&count, "count", false, "print how many records match instead of listing them")
	return cmd
}

// whereArg is --where as the JSON the API takes, or nil when it is not given.
func whereArg(where string) (json.RawMessage, error) {
	if where == "" {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(where), &object); err != nil || object == nil {
		return nil, fmt.Errorf(`--where must be a JSON object, like '{"FIELD_ID": {"eq": "value"}}'`)
	}
	return json.RawMessage(where), nil
}

// orderByArg is --order-by as FIELD, FIELD:asc or FIELD:desc, or nil when it is not given.
func orderByArg(orderBy string) (*RecordOrderInput, error) {
	if orderBy == "" {
		return nil, nil
	}
	field, direction, hasDirection := strings.Cut(orderBy, ":")
	order := &RecordOrderInput{Field: field, Direction: SortDirectionAsc}
	switch {
	case field == "":
		return nil, fmt.Errorf("--order-by needs a field id, createdAt or updatedAt")
	case !hasDirection || direction == "asc":
	case direction == "desc":
		order.Direction = SortDirectionDesc
	default:
		return nil, fmt.Errorf("--order-by takes FIELD, FIELD:asc or FIELD:desc, not %q", orderBy)
	}
	return order, nil
}

func recordGet(command commandFunc) *cobra.Command {
	var target recordTarget
	var id string
	cmd := command("get", "Show one record", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return nil, nil, err
		}
		var found *RecordResponse
		err = s.call(ctx, scope, func(client graphql.Client) (err error) {
			found, err = Record(ctx, client, target.entityID, id)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		if found.Record == nil {
			return nil, nil, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no record " + id + " of entity " + target.entityID}
		}
		record := found.Record.RecordParts
		return map[string]RecordParts{"record": record}, func(out io.Writer) { printRecord(out, record) }, nil
	})
	recordFlags(cmd, &target)
	recordID(cmd, &id)
	return cmd
}

func recordCreate(command commandFunc) *cobra.Command {
	var target recordTarget
	var data string
	cmd := command("create", "Create a record", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		return s.writeRecord(ctx, target, data, func(client graphql.Client, raw json.RawMessage) (RecordParts, error) {
			created, err := CreateRecord(ctx, client, CreateRecordInput{EntityId: target.entityID, Data: raw})
			if err != nil {
				return RecordParts{}, err
			}
			return created.CreateRecord.RecordParts, nil
		})
	})
	recordFlags(cmd, &target)
	cmd.Flags().StringVar(&data, "data", "", "the record's fields, as a JSON object keyed by field id (from model show)")
	_ = cmd.MarkFlagRequired("data")
	return cmd
}

func recordUpdate(command commandFunc) *cobra.Command {
	var target recordTarget
	var id, data, expectedVersion string
	var clear []string
	cmd := command("update", "Change a record: the fields in --data are set, those in --clear are cleared, the rest are kept; with --expected-version only if no one changed it since", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		return s.writeRecord(ctx, target, data, func(client graphql.Client, raw json.RawMessage) (RecordParts, error) {
			updated, err := UpdateRecord(ctx, client, UpdateRecordInput{EntityId: target.entityID, RecordId: id, Data: raw, ClearFieldIds: clear, ExpectedVersion: expectedVersion})
			if err != nil {
				return RecordParts{}, err
			}
			return updated.UpdateRecord.RecordParts, nil
		})
	})
	recordFlags(cmd, &target)
	recordID(cmd, &id)
	previous := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if previous != nil {
			if err := previous(cmd, args); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("expected-version") && expectedVersion == "" {
			return fmt.Errorf("--expected-version is empty: give the record's version from records get or list, or leave --expected-version off to update without a version check")
		}
		return nil
	}
	cmd.Flags().StringVar(&data, "data", "{}", "the fields to set, as a JSON object keyed by field id (from model show)")
	cmd.Flags().StringSliceVar(&clear, "clear", nil, "field ids to clear, comma-separated")
	cmd.Flags().StringVar(&expectedVersion, "expected-version", "", "update only if the record is still at this version, its version from records get or list; otherwise it is refused with CONFLICT and nothing is written")
	return cmd
}

func recordRetire(command commandFunc) *cobra.Command {
	var target recordTarget
	var id string
	cmd := command("retire", "Retire a record", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		return s.writeRecord(ctx, target, "{}", func(client graphql.Client, _ json.RawMessage) (RecordParts, error) {
			retired, err := RetireRecord(ctx, client, RetireRecordInput{EntityId: target.entityID, RecordId: id})
			if err != nil {
				return RecordParts{}, err
			}
			return retired.RetireRecord.RecordParts, nil
		})
	})
	recordFlags(cmd, &target)
	recordID(cmd, &id)
	return cmd
}

func recordUpload(command commandFunc) *cobra.Command {
	var target recordTarget
	var fieldID, path string
	cmd := command("upload <path>", "Upload a file for a file field and print its id, to set on a record with --data", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		size, digest, err := fileDigest(path)
		if err != nil {
			return nil, nil, err
		}
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return nil, nil, err
		}
		name := filepath.Base(path)
		input := RequestRecordFileUploadInput{
			EntityId: target.entityID, FieldId: fieldID, Filename: name,
			ContentType: fileContentType(path), SizeBytes: float64(size), Sha256: digest,
		}
		var requested *RequestRecordFileUploadResponse
		err = s.call(ctx, scope, func(client graphql.Client) (err error) {
			requested, err = RequestRecordFileUpload(ctx, client, input)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		upload := requested.RequestRecordFileUpload
		status, err := put(ctx, path, size, upload.Url, upload.Headers)
		if err != nil || status < 200 || status >= 300 {
			reason := "no response"
			if status != 0 {
				reason = fmt.Sprintf("HTTP %d", status)
			}
			return nil, nil, &cliError{exit: exitRefused, Code: "UPLOAD_REFUSED", Message: fmt.Sprintf(
				"upload of %s refused (%s); nothing was attached. Run records upload again.", name, reason)}
		}
		return map[string]map[string]string{"file": {"id": upload.FileId}}, func(out io.Writer) { fmt.Fprintln(out, upload.FileId) }, nil
	})
	recordFlags(cmd, &target)
	cmd.Args = oneID(&path)
	cmd.Flags().StringVar(&fieldID, "field", "", "the file field's id (from model show)")
	_ = cmd.MarkFlagRequired("field")
	return cmd
}

// recordScope is the scope header the target acts in: the app-tenant's, or with appWide the application's,
// which only an entitlement that reaches the application itself, not one confined to an app-tenant, can give.
func (s *session) recordScope(ctx context.Context, target recordTarget) (string, error) {
	if !target.appWide {
		return s.appScope(ctx, target.appID, target.tenantID)
	}
	if target.envID != "" {
		return s.scopeIn(ctx, target.appID, target.envID)
	}
	entitlements, err := s.entitlements(ctx)
	if err != nil {
		return "", err
	}
	for _, entitlement := range entitlements {
		if entitlement.AppTenantId != "" {
			continue
		}
		apps, err := s.appsIn(ctx, entitlement)
		if err != nil {
			return "", err
		}
		for _, candidate := range apps {
			if candidate.ID == target.appID {
				return entitlement.Client.Id + "/" + target.appID, nil
			}
		}
	}
	return "", &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no application-wide access to application " + target.appID + " that you can reach"}
}

// writeRecord runs one record mutation with data in the target's scope, and reports the record.
// Data that is not JSON is refused before anything is sent; the server refuses JSON that is not an object.
func (s *session) writeRecord(ctx context.Context, target recordTarget, data string, write func(graphql.Client, json.RawMessage) (RecordParts, error)) (any, func(io.Writer), error) {
	if !json.Valid([]byte(data)) {
		return nil, nil, fmt.Errorf("--data is not JSON: %s", data)
	}
	scope, err := s.recordScope(ctx, target)
	if err != nil {
		return nil, nil, err
	}
	var written RecordParts
	err = s.call(ctx, scope, func(client graphql.Client) (err error) {
		written, err = write(client, json.RawMessage(data))
		return err
	})
	result := map[string]RecordParts{"record": written}
	return result, func(out io.Writer) { printRecord(out, written) }, err
}

func recordFlags(cmd *cobra.Command, target *recordTarget) {
	scopeFlags(cmd, target, "act on the application's app-wide records instead of an app-tenant's", true)
	cmd.Flags().StringVar(&target.entityID, "entity", "", "the entity's id (from model show)")
	_ = cmd.MarkFlagRequired("entity")
}

// scopeFlags are --app, --tenant and --app-wide: one app-tenant, by default multipaas.json's test app-tenant,
// or with --app-wide the application itself. With withEnv it adds --env, which names the environment an
// --app-wide target is in.
func scopeFlags(cmd *cobra.Command, target *recordTarget, appWideUsage string, withEnv bool) {
	if withEnv {
		cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
			switch {
			case target.envID != "" && cmd.Flags().Changed("tenant"):
				return fmt.Errorf("--env and --tenant: choose one; a tenant already belongs to one environment")
			case target.envID != "" && !target.appWide:
				return fmt.Errorf("--env needs --app-wide: an app-tenant's records are reached with --tenant, which already belongs to one environment")
			}
			return nil
		}
	}
	appFlag(cmd, &target.appID)
	if withEnv {
		envFlagFromProject(cmd, &target.appID, &target.envID, func() bool { return !target.appWide })
	}
	tenantFlag(cmd, &target.appID, &target.tenantID, func() bool { return target.appWide || target.envID != "" })
	cmd.Flags().BoolVar(&target.appWide, "app-wide", false, appWideUsage)
	cmd.MarkFlagsMutuallyExclusive("tenant", "app-wide")
}

func recordID(cmd *cobra.Command, id *string) {
	cmd.Flags().StringVar(id, "id", "", "the record's id (from records list)")
	_ = cmd.MarkFlagRequired("id")
}

// printRecord prints a record's line, then a line per file it holds: the field, the file's name, type and size,
// and a link that works for 15 minutes.
func printRecord(out io.Writer, record RecordParts) {
	fmt.Fprintf(out, "record %s version %s %s\n", record.Id, record.Version, record.Data)
	for _, file := range record.Files {
		fmt.Fprintf(out, "file %s %s (%s, %.0f bytes) %s\n", file.FieldId, file.Filename, file.ContentType, file.SizeBytes, file.Url)
	}
}

// printRecordFiles adds a table of the listed records' files, when any has one.
func printRecordFiles(out io.Writer, records []RecordParts) {
	type row struct {
		recordID string
		file     recordFile
	}
	rows := []row{}
	for _, record := range records {
		for _, file := range record.Files {
			rows = append(rows, row{recordID: record.Id, file: file})
		}
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(out)
	table(out, "RECORD\tFIELD\tFILE\tBYTES\tURL", rows, func(r row) string {
		return fmt.Sprintf("%s\t%s\t%s\t%.0f\t%s", r.recordID, r.file.FieldId, r.file.Filename, r.file.SizeBytes, r.file.Url)
	})
}

func fileContentType(path string) string {
	if byExtension := mime.TypeByExtension(filepath.Ext(path)); byExtension != "" {
		return byExtension
	}
	return "application/octet-stream"
}
