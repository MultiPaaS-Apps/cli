package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAnUpdateSendsOnlyTheFlagsGivenAndReportsTheAPIVersionAfterIt(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.revisions["app-a-000"] = 4
	got := fake.run(t, "entity", "update", "--app", "app-a-000", "--id", "entity-1", "--name", "Order", "--managed", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"entityId": "entity-1", "name": "Order", "managed": true}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	var printed struct {
		Entity     EntityParts
		APIVersion string `json:"apiVersion"`
	}
	decode(t, got.stdout, &printed)
	if printed.Entity.Id != "entity-1" || printed.Entity.Name != "Order" || printed.APIVersion != "rev-5" {
		t.Fatalf("printed %s", got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
}

func TestFieldAddSendsAnEnumTypeIdAndBooleansAsTheirTypes(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", "enum", "--enum-type", "enum-1", "--required", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"entityId": "entity-1", "type": "enum", "enumTypeId": "enum-1", "required": true}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestFieldAddSendsAFileTypeWithItsMaximumSizeAsANumber(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", "file", "--max-file-bytes", "1048576", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"entityId": "entity-1", "type": "file", "maxFileBytes": float64(1048576)}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestFieldUpdateSendsOnlyTheMaximumSize(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--max-file-bytes", "2048", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"fieldId": "field-1", "maxFileBytes": float64(2048)}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestFieldUpdateReadsTheFieldsTypeToSendItsDefault(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.fields["entity-1"] = []map[string]any{{"id": "field-1", "graphqlFieldName": "priority", "type": "number", "notNull": false}}
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--default", "5", "--not-null", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"fieldId": "field-1", "defaultValue": float64(5), "notNull": true}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
}

func TestFieldUpdateUsesTheTypeGivenForItsDefault(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.fields["entity-1"] = []map[string]any{{"id": "field-1", "graphqlFieldName": "priority", "type": "text", "notNull": false}}
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--type", "boolean", "--default", "true", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"fieldId": "field-1", "type": "boolean", "defaultValue": true}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestFieldUpdateClearsTheDefaultAndNotNull(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--clear-default", "--nullable", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"fieldId": "field-1", "defaultValue": nil, "notNull": false}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestFieldUpdateSendsNothingForAClearOrNullableFlagGivenAsFalse(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--clear-default=false", "--nullable=false", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"fieldId": "field-1"}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
}

func TestFieldUpdateRefusesConflictingDefaultAndNotNullFlags(t *testing.T) {
	for _, c := range []struct {
		flags   []string
		message string
	}{
		{[]string{"--default", "1", "--clear-default"}, "if any flags in the group [clear-default default] are set none of the others can be; [clear-default default] were all set"},
		{[]string{"--not-null", "--nullable"}, "if any flags in the group [nullable not-null] are set none of the others can be; [not-null nullable] were all set"},
	} {
		fake := signedInFake(t, fresh)
		fake.fields["entity-1"] = []map[string]any{{"id": "field-1", "graphqlFieldName": "priority", "type": "number", "notNull": false}}
		args := append([]string{"field", "update", "--app", "app-a-000", "--id", "field-1"}, c.flags...)
		got := fake.run(t, args...)
		if got.exit == exitOK || !strings.Contains(got.stdout+got.stderr, c.message) || len(fake.mutations) != 0 {
			t.Fatalf("%v: exit %d, printed %q %q, sent %v", c.flags, got.exit, got.stdout, got.stderr, fake.mutations)
		}
	}
}

func TestModelShowPrintsAFileFieldsMaximumSize(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.entities["app-a-000"] = append(fake.entities["app-a-000"], map[string]any{"id": "entity-1", "name": "Order"})
	fake.fields["entity-1"] = []map[string]any{
		{"id": "field-1", "graphqlFieldName": "receipt", "type": "file", "maxFileBytes": 1048576},
		{"id": "field-2", "graphqlFieldName": "note", "type": "text"},
	}
	got := fake.run(t, "model", "show", "--app", "app-a-000")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if !strings.Contains(got.stdout, "file (max 1048576 bytes)") {
		t.Fatalf("shown %q", got.stdout)
	}
	if strings.Contains(got.stdout, "text (max") {
		t.Fatalf("a field without a maximum printed one: %q", got.stdout)
	}
}

func TestModelShowPrintsEachFieldsDefaultAndNotNull(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.entities["app-a-000"] = append(fake.entities["app-a-000"], map[string]any{"id": "entity-1", "name": "Ticket"})
	fake.fields["entity-1"] = []map[string]any{
		{"id": "field-1", "graphqlFieldName": "priority", "type": "number", "notNull": true, "defaultValue": 3},
		{"id": "field-2", "graphqlFieldName": "code", "type": "text", "defaultValue": "3"},
		{"id": "field-3", "graphqlFieldName": "note", "type": "text"},
	}
	got := fake.run(t, "model", "show", "--app", "app-a-000")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	for _, want := range []string{"NOT NULL", "DEFAULT"} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("no %s column: %q", want, got.stdout)
		}
	}
	lines := map[string][]string{}
	for _, line := range strings.Split(got.stdout, "\n") {
		if words := strings.Fields(line); len(words) == 6 {
			lines[words[0]] = words
		}
	}
	for name, want := range map[string][]string{
		"priority": {"priority", "number", "false", "true", "3", "field-1"},
		"code":     {"code", "text", "false", "false", `"3"`, "field-2"},
		"note":     {"note", "text", "false", "false", "-", "field-3"},
	} {
		if !reflect.DeepEqual(lines[name], want) {
			t.Fatalf("%s printed %v, want %v, in %q", name, lines[name], want, got.stdout)
		}
	}
}

func TestFieldAddSendsANumberDefaultAsANumberAndNotNull(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", "number", "--name", "Priority", "--default", "3", "--not-null", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"entityId": "entity-1", "type": "number", "name": "Priority", "defaultValue": float64(3), "notNull": true}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestFieldAddSendsEachTypesDefaultAsItsValue(t *testing.T) {
	for _, c := range []struct {
		fieldType, given string
		want             any
	}{
		{"boolean", "true", true},
		{"boolean", "false", false},
		{"text", "3", "3"},
		{"enum", "low", "low"},
		{"timestamp", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"},
		{"reference", "8f0c2a4e-1b7d-4c55-9a3e-0d6f1e2b3c4d", "8f0c2a4e-1b7d-4c55-9a3e-0d6f1e2b3c4d"},
	} {
		fake := signedInFake(t, fresh)
		got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", c.fieldType, "--default", c.given, "--json")
		if got.exit != exitOK {
			t.Fatalf("%s: exit %d: %s", c.fieldType, got.exit, got.stdout)
		}
		input := fake.mutations[0]["input"].(map[string]any)
		if !reflect.DeepEqual(input["defaultValue"], c.want) {
			t.Fatalf("%s --default %s sent %#v, want %#v", c.fieldType, c.given, input["defaultValue"], c.want)
		}
	}
}

func TestFieldAddRefusesABadDefaultBeforeSendingAnything(t *testing.T) {
	for _, c := range []struct{ fieldType, given, message string }{
		{"number", "three", `--default for a number field must be a number, not "three"`},
		{"boolean", "yes", `--default for a boolean field must be true or false, not "yes"`},
		{"file", "x", "a file field can't have a default"},
		{"related", "x", "a related field can't have a default"},
	} {
		t.Run(c.fieldType, func(t *testing.T) {
			fake := signedInFake(t, fresh)
			got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", c.fieldType, "--default", c.given, "--json")
			assertFailure(t, got, exitRefused, "CLI_ERROR")
			var failure struct{ Error cliError }
			decode(t, got.stdout, &failure)
			if failure.Error.Message != c.message {
				t.Fatalf("%s --default %s printed %q, want %q", c.fieldType, c.given, failure.Error.Message, c.message)
			}
			if len(fake.mutations) != 0 {
				t.Fatalf("%s: sent %v", c.fieldType, fake.mutations)
			}
		})
	}
}

func TestFieldAddSendsList(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", "text", "--name", "Tags", "--list", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"entityId": "entity-1", "type": "text", "name": "Tags", "list": true}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestFieldAddSendsAListDefaultAsAnArray(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", "text", "--list", "--default", `["a","b"]`, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	input := fake.mutations[0]["input"].(map[string]any)
	if want := []any{"a", "b"}; !reflect.DeepEqual(input["defaultValue"], want) {
		t.Fatalf("sent %#v, want %#v", input["defaultValue"], want)
	}
}

func TestFieldAddRefusesAListDefaultThatIsNotAnArray(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", "text", "--list", "--default", "a", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	var failure struct{ Error cliError }
	decode(t, got.stdout, &failure)
	if want := `--default for a list field must be a JSON array of strings, such as '["a","b"]', not "a"`; failure.Error.Message != want {
		t.Fatalf("printed %q, want %q", failure.Error.Message, want)
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestFieldAddRefusesANullListDefault(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", "text", "--list", "--default", "null", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestFieldUpdateRefusesANullListDefault(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.fields["entity-1"] = []map[string]any{{"id": "field-1", "graphqlFieldName": "tags", "type": "text", "list": true}}
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--default", "null", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestFieldUpdateReadsTheFieldsListToSendItsDefaultAsAnArray(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.fields["entity-1"] = []map[string]any{{"id": "field-1", "graphqlFieldName": "tags", "type": "text", "list": true}}
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--type", "text", "--default", `["a"]`, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	input := fake.mutations[0]["input"].(map[string]any)
	if want := []any{"a"}; !reflect.DeepEqual(input["defaultValue"], want) {
		t.Fatalf("sent %#v, want %#v", input["defaultValue"], want)
	}
}

func TestFieldUpdateHasNoListFlag(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--list", "--json")
	if got.exit == exitOK || !strings.Contains(got.stdout+got.stderr, "unknown flag: --list") {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestModelShowMarksAListField(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.entities["app-a-000"] = append(fake.entities["app-a-000"], map[string]any{"id": "entity-1", "name": "Ticket"})
	fake.fields["entity-1"] = []map[string]any{
		{"id": "field-1", "graphqlFieldName": "tags", "type": "text", "list": true},
		{"id": "field-2", "graphqlFieldName": "note", "type": "text", "list": false},
	}
	got := fake.run(t, "model", "show", "--app", "app-a-000")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	types := map[string]string{}
	for _, line := range strings.Split(got.stdout, "\n") {
		if words := strings.Fields(line); len(words) == 6 {
			types[words[0]] = words[1]
		}
	}
	if types["tags"] != "list-of-text" || types["note"] != "text" {
		t.Fatalf("types %v in %q", types, got.stdout)
	}
}

func TestEnumCreateSendsItsValuesWithAnOptionalGraphQLName(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "enum", "create", "--app", "app-a-000", "--name", "Status", "--values", "In progress,N/A=NOT_APPLICABLE", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"name": "Status", "values": []any{
		map[string]any{"value": "In progress"},
		map[string]any{"value": "N/A", "graphqlName": "NOT_APPLICABLE"},
	}}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
	var printed struct {
		EnumType   EnumTypeParts
		APIVersion string `json:"apiVersion"`
	}
	decode(t, got.stdout, &printed)
	if printed.EnumType.Id != "new-1" || printed.EnumType.Name != "Status" || printed.APIVersion != "rev-1" {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestEnumValuesAreTrimmedAndSplitOnTheLastEquals(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "enum", "create", "--app", "app-a-000", "--name", "Status", "--values", "Open, In progress,N/A = NOT_APPLICABLE,a=b=A_EQ_B", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"name": "Status", "values": []any{
		map[string]any{"value": "Open"},
		map[string]any{"value": "In progress"},
		map[string]any{"value": "N/A", "graphqlName": "NOT_APPLICABLE"},
		map[string]any{"value": "a=b", "graphqlName": "A_EQ_B"},
	}}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestEnumUpdateSendsOnlyWhatWasNamedAndRetireSendsTheId(t *testing.T) {
	fake := signedInFake(t, fresh)
	updated := fake.run(t, "enum", "update", "--app", "app-a-000", "--id", "enum-1", "--name", "State", "--json")
	retired := fake.run(t, "enum", "retire", "--app", "app-a-000", "--id", "enum-1", "--json")
	if updated.exit != exitOK || retired.exit != exitOK {
		t.Fatalf("exits %d %d: %s %s", updated.exit, retired.exit, updated.stdout, retired.stdout)
	}
	if want := (map[string]any{"input": map[string]any{"enumTypeId": "enum-1", "name": "State"}}); !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("update sent %v, want %v", fake.mutations[0], want)
	}
	if want := (map[string]any{"enumTypeId": "enum-1"}); !reflect.DeepEqual(fake.mutations[1], want) {
		t.Fatalf("retire sent %v, want %v", fake.mutations[1], want)
	}
}

func TestEnumCreateWithoutModelEditIsRefusedUnauthorized(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.noModelEdit = true
	assertFailure(t, fake.run(t, "enum", "create", "--app", "app-a-000", "--name", "Status", "--values", "Open", "--json"), exitRefused, "UNAUTHORIZED")
}

func TestARetireSendsTheIdAsItsArgument(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "constraint", "retire", "--app", "app-a-000", "--id", "constraint-1", "--json")
	if got.exit != exitOK || !reflect.DeepEqual(fake.mutations[0], map[string]any{"constraintId": "constraint-1"}) {
		t.Fatalf("exit %d sent %v: %s", got.exit, fake.mutations, got.stdout)
	}
}

func TestAModelChangeInAnAppTenantEntitlementSendsItsAppTenantScope(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "entity", "create", "--app", "app-b", "--name", "Order", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-b/app-b/tenant-b" {
		t.Fatalf("scope %q", last)
	}
}

func TestARefusedModelChangeIsExitOneWithThePlatformsCodeAndMessage(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.refuseChange = [2]string{"UNSAFE_FIELD_CHANGE", "can't change type while the entity has records"}
	got := fake.run(t, "field", "update", "--app", "app-a-000", "--id", "field-1", "--type", "number", "--json")
	assertFailure(t, got, exitRefused, "UNSAFE_FIELD_CHANGE")
	if !strings.Contains(got.stdout, "can't change type while the entity has records") || fake.revisions["app-a-000"] != 0 {
		t.Fatalf("stdout %s, revisions %d", got.stdout, fake.revisions["app-a-000"])
	}
}

func TestAMemberWithoutModelEditIsRefusedUnauthorized(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.noModelEdit = true
	got := fake.run(t, "entity", "create", "--app", "app-a-000", "--name", "Order", "--json")
	assertFailure(t, got, exitRefused, "UNAUTHORIZED")
	if fake.revisions["app-a-000"] != 0 {
		t.Fatalf("a refused change minted a revision")
	}
}

func TestAModelCommandForAnAppYouCannotReachSendsNothing(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "entity", "retire", "--app", "app-z", "--id", "entity-1", "--json")
	assertFailure(t, got, exitRefused, "NOT_FOUND")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestModelShowReadsEveryEntityWithItsFieldsAndConstraints(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.revisions["app-a-000"] = 2
	for i := range 101 {
		fake.entities["app-a-000"] = append(fake.entities["app-a-000"], map[string]any{"id": fmt.Sprintf("entity-%03d", i), "name": "E"})
	}
	fake.enumTypes["app-a-000"] = []map[string]any{{"id": "enum-1", "name": "Status", "graphqlTypeName": "Status", "values": []map[string]any{{"value": "open", "graphqlName": "OPEN"}}}}
	fake.fields["entity-100"] = []map[string]any{{"id": "field-1", "graphqlFieldName": "status", "type": "enum", "enumType": map[string]any{"id": "enum-1", "name": "Status"}}}
	fake.constraints["entity-100"] = []map[string]any{{"id": "constraint-1", "kind": "compositeUnique", "fields": map[string]any{"items": []map[string]any{{"id": "field-1"}}}}}
	got := fake.run(t, "model", "show", "--app", "app-a-000", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	var shown shownModel
	decode(t, got.stdout, &shown)
	last := shown.Entities[len(shown.Entities)-1]
	if shown.APIVersion != "rev-2" || len(shown.Entities) != 101 || last.Fields[0].Id != "field-1" || last.Constraints[0].Fields.Items[0].Id != "field-1" ||
		last.Fields[0].EnumType == nil || last.Fields[0].EnumType.Id != "enum-1" ||
		len(shown.Enums) != 1 || shown.Enums[0].Id != "enum-1" || shown.Enums[0].Values[0].GraphqlName != "OPEN" {
		t.Fatalf("shown %s", got.stdout)
	}
}

func TestAChangeWhoseVersionCannotBeReadStillReportsTheChange(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.failVersion = true
	got := fake.run(t, "entity", "create", "--app", "app-a-000", "--name", "Order", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	var printed struct {
		Entity     EntityParts
		APIVersion string `json:"apiVersion"`
	}
	decode(t, got.stdout, &printed)
	if printed.Entity.Id != "new-1" || printed.APIVersion != "" || fake.revisions["app-a-000"] != 1 {
		t.Fatalf("printed %s, revisions %d", got.stdout, fake.revisions["app-a-000"])
	}
}

func TestAChangeWhoseVersionCannotBeReadSaysSoInItsHumanOutput(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.failVersion = true
	got := fake.run(t, "entity", "create", "--app", "app-a-000", "--name", "Order")
	if got.exit != exitOK || !strings.Contains(got.stdout, "entity new-1") || !strings.Contains(got.stdout, "could not be read") || strings.Contains(got.stdout, "API version \n") {
		t.Fatalf("exit %d: %q", got.exit, got.stdout)
	}
}
