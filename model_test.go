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

func TestFieldAddSendsListsAndBooleansAsTheirTypes(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "field", "add", "--app", "app-a-000", "--entity", "entity-1", "--type", "enum", "--enum-values", "open,closed", "--required", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"entityId": "entity-1", "type": "enum", "enumValues": []any{"open", "closed"}, "required": true}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
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
	fake.fields["entity-100"] = []map[string]any{{"id": "field-1", "graphqlFieldName": "status", "type": "enum", "enumValues": []string{"open"}}}
	fake.constraints["entity-100"] = []map[string]any{{"id": "constraint-1", "kind": "compositeUnique", "fields": map[string]any{"items": []map[string]any{{"id": "field-1"}}}}}
	got := fake.run(t, "model", "show", "--app", "app-a-000", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	var shown shownModel
	decode(t, got.stdout, &shown)
	last := shown.Entities[len(shown.Entities)-1]
	if shown.APIVersion != "rev-2" || len(shown.Entities) != 101 || last.Fields[0].Id != "field-1" || last.Constraints[0].Fields.Items[0].Id != "field-1" {
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
