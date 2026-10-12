package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// commandFunc builds one leaf command; run passes its own, so model commands share --json and reporting.
type commandFunc func(use, short string, do func(context.Context, *session) (any, func(io.Writer), error)) *cobra.Command

// option is one input a model command takes: its flag, the input field it sets, its help, and its kind
// ("string", "bool" or "list").
type option struct {
	flag, field, usage, kind string
	required                 bool
}

// row is what a model change returns: an entity, a field or a constraint.
type row interface{ GetId() string }

// mutation runs one model mutation with the command's input and returns the row it changed.
type mutation func(ctx context.Context, client graphql.Client, input map[string]any) (row, error)

// modelCommands are model show and the entity, field and constraint commands. Each change prints the
// row it changed and the API version the model is at after it.
func modelCommands(command commandFunc) []*cobra.Command {
	entityID := option{flag: "id", field: "entityId", usage: "the entity's id (from model show)", required: true}
	fieldID := option{flag: "id", field: "fieldId", usage: "the field's id (from model show)", required: true}
	fieldOptions := []option{
		{flag: "name", field: "name", usage: "the field's display name"},
		{flag: "api-name", field: "graphqlFieldName", usage: "its GraphQL field name (default: from the name)"},
		{flag: "required", field: "required", usage: "a record must have a value", kind: "bool"},
		{flag: "enum-values", field: "enumValues", usage: "an enum field's values, comma-separated", kind: "list"},
		{flag: "references", field: "referencesEntityId", usage: "a reference field's target entity id"},
		{flag: "related", field: "relatedFieldId", usage: "a related field's reference field id"},
		{flag: "parent", field: "isParent", usage: "a reference to the record's parent", kind: "bool"},
	}
	fieldType := option{flag: "type", field: "type", usage: "text, number, boolean, timestamp, enum, reference or related"}
	return []*cobra.Command{
		group("model", "An application's model", modelShow(command)),
		group("entity", "Declare, change and retire entities",
			change(command, "create", "Declare an entity", "entity", createEntity,
				option{flag: "name", field: "name", usage: "the entity's name", required: true},
				option{flag: "type-name", field: "graphqlTypeName", usage: "its GraphQL type name (default: from the name)"},
				option{flag: "plural-name", field: "graphqlPluralName", usage: "its GraphQL plural name (default: from the name)"}),
			change(command, "update", "Rename an entity or change whether it is managed", "entity", updateEntity,
				entityID,
				option{flag: "name", field: "name", usage: "the entity's new name"},
				option{flag: "type-name", field: "graphqlTypeName", usage: "its new GraphQL type name"},
				option{flag: "plural-name", field: "graphqlPluralName", usage: "its new GraphQL plural name"},
				option{flag: "managed", field: "managed", usage: "whether the entity is managed", kind: "bool"}),
			change(command, "retire", "Retire an entity", "entity", retireEntity, entityID),
		),
		group("field", "Declare, change and retire fields",
			change(command, "add", "Add a field to an entity", "field", createField, append([]option{
				{flag: "entity", field: "entityId", usage: "the entity's id (from model show)", required: true},
				{flag: fieldType.flag, field: fieldType.field, usage: fieldType.usage, required: true},
				{flag: "lifecycle", field: "isLifecycle", usage: "the entity's lifecycle field", kind: "bool"},
			}, fieldOptions...)...),
			change(command, "update", "Change a field", "field", updateField, append([]option{fieldID, fieldType}, fieldOptions...)...),
			change(command, "retire", "Retire a field", "field", retireField, fieldID),
		),
		group("constraint", "Declare and retire unique constraints",
			change(command, "add", "Make a set of an entity's fields unique together", "constraint", createConstraint,
				option{flag: "entity", field: "entityId", usage: "the entity's id (from model show)", required: true},
				option{flag: "fields", field: "fieldIds", usage: "the field ids, comma-separated", kind: "list", required: true}),
			change(command, "retire", "Retire a constraint", "constraint", retireConstraint,
				option{flag: "id", field: "constraintId", usage: "the constraint's id (from model show)", required: true}),
		),
	}
}

func modelShow(command commandFunc) *cobra.Command {
	var appID string
	show := command("show", "Show an application's entities, fields and constraints", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		shown, err := s.model(ctx, appID)
		return shown, func(out io.Writer) { printModel(out, shown) }, err
	})
	appFlag(show, &appID)
	return show
}

// change is one model command: it sends its mutation with the flags given, in the app's scope.
func change(command commandFunc, use, short, noun string, mutate mutation, options ...option) *cobra.Command {
	var appID string
	var cmd *cobra.Command
	cmd = command(use, short, func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		input := readInput(cmd, options)
		var changed row
		version, err := s.change(ctx, appID, func(client graphql.Client) (err error) {
			changed, err = mutate(ctx, client, input)
			return err
		})
		result := map[string]any{noun: changed, "apiVersion": version}
		return result, func(out io.Writer) {
			fmt.Fprintf(out, "%s %s\n", noun, changed.GetId())
			if version == "" {
				fmt.Fprintln(out, "Changed; its API version could not be read. Run model show for the current version.")
				return
			}
			fmt.Fprintf(out, "API version %s\n", version)
		}, err
	})
	appFlag(cmd, &appID)
	for _, o := range options {
		switch o.kind {
		case "bool":
			cmd.Flags().Bool(o.flag, false, o.usage)
		case "list":
			cmd.Flags().StringSlice(o.flag, nil, o.usage)
		default:
			cmd.Flags().String(o.flag, "", o.usage)
		}
		if o.required {
			_ = cmd.MarkFlagRequired(o.flag)
		}
	}
	return cmd
}

// readInput is the mutation input from the flags given on the command line, so an update changes only what was named.
func readInput(cmd *cobra.Command, options []option) map[string]any {
	input := map[string]any{}
	for _, o := range options {
		if !cmd.Flags().Changed(o.flag) {
			continue
		}
		switch o.kind {
		case "bool":
			input[o.field], _ = cmd.Flags().GetBool(o.flag)
		case "list":
			input[o.field], _ = cmd.Flags().GetStringSlice(o.flag)
		default:
			input[o.field], _ = cmd.Flags().GetString(o.flag)
		}
	}
	return input
}

func createEntity(ctx context.Context, client graphql.Client, input map[string]any) (row, error) {
	created, err := CreateEntity(ctx, client, input)
	if err != nil {
		return nil, err
	}
	return &created.CreateEntity.EntityParts, nil
}

func updateEntity(ctx context.Context, client graphql.Client, input map[string]any) (row, error) {
	updated, err := UpdateEntity(ctx, client, input)
	if err != nil {
		return nil, err
	}
	return &updated.UpdateEntity.EntityParts, nil
}

func retireEntity(ctx context.Context, client graphql.Client, input map[string]any) (row, error) {
	retired, err := RetireEntity(ctx, client, input["entityId"].(string))
	if err != nil {
		return nil, err
	}
	return &retired.RetireEntity.EntityParts, nil
}

func createField(ctx context.Context, client graphql.Client, input map[string]any) (row, error) {
	created, err := CreateField(ctx, client, input)
	if err != nil {
		return nil, err
	}
	return &created.CreateField.FieldParts, nil
}

func updateField(ctx context.Context, client graphql.Client, input map[string]any) (row, error) {
	updated, err := UpdateField(ctx, client, input)
	if err != nil {
		return nil, err
	}
	return &updated.UpdateField.FieldParts, nil
}

func retireField(ctx context.Context, client graphql.Client, input map[string]any) (row, error) {
	retired, err := RetireField(ctx, client, input["fieldId"].(string))
	if err != nil {
		return nil, err
	}
	return &retired.RetireField.FieldParts, nil
}

func createConstraint(ctx context.Context, client graphql.Client, input map[string]any) (row, error) {
	created, err := CreateConstraint(ctx, client, input)
	if err != nil {
		return nil, err
	}
	return &created.CreateConstraint.ConstraintParts, nil
}

func retireConstraint(ctx context.Context, client graphql.Client, input map[string]any) (row, error) {
	retired, err := RetireConstraint(ctx, client, input["constraintId"].(string))
	if err != nil {
		return nil, err
	}
	return &retired.RetireConstraint.ConstraintParts, nil
}

// shownModel is model show's answer: the API version the model is at ("" before the first change) and its entities.
type shownModel struct {
	APIVersion string        `json:"apiVersion"`
	Entities   []shownEntity `json:"entities"`
}

type shownEntity struct {
	EntityParts
	Fields      []FieldParts      `json:"fields"`
	Constraints []ConstraintParts `json:"constraints"`
}

// model reads appID's model. The version is read first, so the model shown is at least that version.
func (s *session) model(ctx context.Context, appID string) (shownModel, error) {
	scope, err := s.appScope(ctx, appID, "")
	if err != nil {
		return shownModel{}, err
	}
	version, err := s.latestVersion(ctx, scope)
	if err != nil {
		return shownModel{}, err
	}
	entities, err := pages(func(page PageInput) ([]EntityParts, bool, error) {
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
	if err != nil {
		return shownModel{}, err
	}
	shown := shownModel{APIVersion: version, Entities: []shownEntity{}}
	for _, entity := range entities {
		fields, err := pages(func(page PageInput) ([]FieldParts, bool, error) {
			var listed *FieldsResponse
			err := s.call(ctx, scope, func(client graphql.Client) (err error) {
				listed, err = Fields(ctx, client, entity.Id, page)
				return err
			})
			if err != nil {
				return nil, false, err
			}
			items := []FieldParts{}
			for _, item := range listed.Fields.Items {
				items = append(items, item.FieldParts)
			}
			return items, listed.Fields.PageInfo.HasMore, nil
		})
		if err != nil {
			return shownModel{}, err
		}
		constraints, err := pages(func(page PageInput) ([]ConstraintParts, bool, error) {
			var listed *ConstraintsResponse
			err := s.call(ctx, scope, func(client graphql.Client) (err error) {
				listed, err = Constraints(ctx, client, entity.Id, page)
				return err
			})
			if err != nil {
				return nil, false, err
			}
			items := []ConstraintParts{}
			for _, item := range listed.Constraints.Items {
				items = append(items, item.ConstraintParts)
			}
			return items, listed.Constraints.PageInfo.HasMore, nil
		})
		if err != nil {
			return shownModel{}, err
		}
		shown.Entities = append(shown.Entities, shownEntity{EntityParts: entity, Fields: fields, Constraints: constraints})
	}
	return shown, nil
}

// change runs one model mutation in appID's scope, then reads the API version the model is at after it.
// The version is "" when the change committed but its version couldn't be read.
// ponytail: the newest revision, not the one this write minted; someone else's change in between is reported instead.
// The exact id needs the mutations to return it.
func (s *session) change(ctx context.Context, appID string, mutate func(graphql.Client) error) (string, error) {
	scope, err := s.appScope(ctx, appID, "")
	if err != nil {
		return "", err
	}
	if err := s.call(ctx, scope, mutate); err != nil {
		return "", err
	}
	version, err := s.latestVersion(ctx, scope)
	if err != nil {
		return "", nil
	}
	return version, nil
}

// latestVersion is the id of the model's newest revision, or "" when it has none.
func (s *session) latestVersion(ctx context.Context, scope string) (string, error) {
	var latest *LatestModelRevisionResponse
	err := s.call(ctx, scope, func(client graphql.Client) (err error) {
		latest, err = LatestModelRevision(ctx, client)
		return err
	})
	if err != nil || len(latest.ModelRevisions.Items) == 0 {
		return "", err
	}
	return latest.ModelRevisions.Items[0].Id, nil
}

// appScope is the scope sent for appID, or for its app-tenant tenantID when that is not "": the first
// entitlement that reaches it. An app-tenant-level entitlement reaches only its own app-tenant.
func (s *session) appScope(ctx context.Context, appID, tenantID string) (string, error) {
	entitlements, err := s.entitlements(ctx)
	if err != nil {
		return "", err
	}
	for _, entitlement := range entitlements {
		if entitlement.AppTenantId != "" {
			if entitlement.ApplicationId == appID && (tenantID == "" || tenantID == entitlement.AppTenantId) {
				return entitlement.Client.Id + "/" + appID + "/" + entitlement.AppTenantId, nil
			}
			continue
		}
		apps, err := s.appsIn(ctx, entitlement)
		if err != nil {
			return "", err
		}
		for _, candidate := range apps {
			if candidate.ID == appID {
				return strings.TrimSuffix(entitlement.Client.Id+"/"+appID+"/"+tenantID, "/"), nil
			}
		}
	}
	if tenantID != "" {
		return "", &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no app-tenant " + tenantID + " of application " + appID + " that you can reach"}
	}
	return "", &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no application " + appID + " that you can reach"}
}

// inApp runs one operation in appID's scope.
func (s *session) inApp(ctx context.Context, appID string, operation func(graphql.Client) error) error {
	scope, err := s.appScope(ctx, appID, "")
	if err != nil {
		return err
	}
	return s.call(ctx, scope, operation)
}

func printModel(out io.Writer, shown shownModel) {
	fmt.Fprintf(out, "API version %s\n", shown.APIVersion)
	for _, entity := range shown.Entities {
		fmt.Fprintf(out, "\n%s (%s, managed %t)  %s\n", entity.Name, entity.GraphqlTypeName, entity.Managed, entity.Id)
		table(out, "  FIELD\tTYPE\tREQUIRED\tID", entity.Fields, func(f FieldParts) string {
			return "  " + f.GraphqlFieldName + "\t" + f.Type + "\t" + strconv.FormatBool(f.Required) + "\t" + f.Id
		})
		for _, constraint := range entity.Constraints {
			ids := []string{}
			for _, field := range constraint.Fields.Items {
				ids = append(ids, field.Id)
			}
			fmt.Fprintf(out, "  %s on %s  %s\n", constraint.Kind, strings.Join(ids, ", "), constraint.Id)
		}
	}
}

func group(use, short string, children ...*cobra.Command) *cobra.Command {
	parent := &cobra.Command{Use: use, Short: short}
	parent.AddCommand(children...)
	return parent
}

// appFlag is --app, which defaults to multipaas.json's applicationId.
func appFlag(cmd *cobra.Command, appID *string) {
	cmd.Flags().StringVar(appID, "app", "", "the application's id (from apps list; default: multipaas.json's applicationId)")
	defaultFrom(cmd, appID, "app", func(config projectConfig) string { return config.ApplicationID })
}
