package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// recordTarget is the app-tenant and entity a records command acts on.
type recordTarget struct{ appID, tenantID, entityID string }

// recordCommands read and write an entity's records in one app-tenant, as the developer: test data for
// an app built on the application. A record's data is a JSON object keyed by field id.
func recordCommands(command commandFunc) *cobra.Command {
	return group("records", "Read and write an entity's records in an app-tenant",
		recordsList(command), recordCreate(command), recordUpdate(command), recordRetire(command))
}

func recordsList(command commandFunc) *cobra.Command {
	var target recordTarget
	cmd := command("list", "List an entity's records, newest first", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.appScope(ctx, target.appID, target.tenantID)
		if err != nil {
			return nil, nil, err
		}
		list, err := pages(func(page PageInput) ([]RecordParts, bool, error) {
			var listed *RecordsResponse
			err := s.call(ctx, scope, func(client graphql.Client) (err error) {
				listed, err = Records(ctx, client, target.entityID, page)
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
			table(out, "ID\tUPDATED\tDATA", list, func(r RecordParts) string { return r.Id + "\t" + r.UpdatedAt + "\t" + string(r.Data) })
		}, err
	})
	recordFlags(cmd, &target)
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
	var id, data string
	var clear []string
	cmd := command("update", "Change a record: the fields in --data are set, those in --clear are cleared, the rest are kept", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		return s.writeRecord(ctx, target, data, func(client graphql.Client, raw json.RawMessage) (RecordParts, error) {
			updated, err := UpdateRecord(ctx, client, UpdateRecordInput{EntityId: target.entityID, RecordId: id, Data: raw, ClearFieldIds: clear})
			if err != nil {
				return RecordParts{}, err
			}
			return updated.UpdateRecord.RecordParts, nil
		})
	})
	recordFlags(cmd, &target)
	recordID(cmd, &id)
	cmd.Flags().StringVar(&data, "data", "{}", "the fields to set, as a JSON object keyed by field id (from model show)")
	cmd.Flags().StringSliceVar(&clear, "clear", nil, "field ids to clear, comma-separated")
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

// writeRecord runs one record mutation with data in the target's app-tenant, and reports the record.
// Data that is not JSON is refused before anything is sent; the server refuses JSON that is not an object.
func (s *session) writeRecord(ctx context.Context, target recordTarget, data string, write func(graphql.Client, json.RawMessage) (RecordParts, error)) (any, func(io.Writer), error) {
	if !json.Valid([]byte(data)) {
		return nil, nil, fmt.Errorf("--data is not JSON: %s", data)
	}
	scope, err := s.appScope(ctx, target.appID, target.tenantID)
	if err != nil {
		return nil, nil, err
	}
	var written RecordParts
	err = s.call(ctx, scope, func(client graphql.Client) (err error) {
		written, err = write(client, json.RawMessage(data))
		return err
	})
	result := map[string]RecordParts{"record": written}
	return result, func(out io.Writer) { fmt.Fprintf(out, "record %s %s\n", written.Id, written.Data) }, err
}

func recordFlags(cmd *cobra.Command, target *recordTarget) {
	appFlag(cmd, &target.appID)
	tenantFlag(cmd, &target.appID, &target.tenantID)
	cmd.Flags().StringVar(&target.entityID, "entity", "", "the entity's id (from model show)")
	_ = cmd.MarkFlagRequired("entity")
}

func recordID(cmd *cobra.Command, id *string) {
	cmd.Flags().StringVar(id, "id", "", "the record's id (from records list)")
	_ = cmd.MarkFlagRequired("id")
}
