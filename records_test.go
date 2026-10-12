package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRecordsCreateWritesInTheProjectsTestAppTenant(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	got := fake.run(t, "records", "create", "--entity", "entity-1", "--data", `{"field-1":"Ship it"}`, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"entityId": "entity-1", "data": map[string]any{"field-1": "Ship it"}}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
	if compact(got.stdout) != `{"record":{"id":"record-1","data":{"field-1":"Ship it"},"createdAt":"2026-10-11T00:00:00.000Z","updatedAt":"2026-10-11T00:00:00.000Z"}}` {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsUpdateSendsTheDataAndClearsGivenAndKeepsTheRest(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{{"id": "record-1", "data": map[string]any{"field-1": "a", "field-2": "b"}, "createdAt": "x", "updatedAt": "x"}}
	got := fake.run(t, "records", "update", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1",
		"--data", `{"field-3":3}`, "--clear", "field-1", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"entityId": "entity-1", "recordId": "record-1", "data": map[string]any{"field-3": float64(3)}, "clearFieldIds": []any{"field-1"}}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
	listed := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json")
	var records []struct{ Data map[string]any }
	decode(t, listed.stdout, &records)
	if !reflect.DeepEqual(records[0].Data, map[string]any{"field-2": "b", "field-3": float64(3)}) {
		t.Fatalf("listed %s", listed.stdout)
	}
}

func TestRecordsListCollectsEveryPage(t *testing.T) {
	fake := projectFake(t)
	for i := range 101 {
		fake.records["entity-1"] = append(fake.records["entity-1"], map[string]any{"id": fmt.Sprintf("record-%d", i), "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"})
	}
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json")
	var records []RecordParts
	decode(t, got.stdout, &records)
	if len(records) != 101 {
		t.Fatalf("listed %d records", len(records))
	}
}

func TestRecordDataThatIsNotJSONSendsNothing(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "create", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--data", `{"field-1":`, "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "--data is not JSON") {
		t.Fatalf("printed %s", got.stdout)
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestARecordCommandInAnAppTenantEntitlementReachesOnlyItsOwnAppTenant(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "retire", "--app", "app-b", "--tenant", "tenant-b", "--entity", "entity-1", "--id", "record-9", "--json")
	assertFailure(t, got, exitRefused, "NOT_FOUND") // the fake's own refusal: the record isn't there
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-b/app-b/tenant-b" {
		t.Fatalf("scope %q", last)
	}
	fake.mutations = nil
	got = fake.run(t, "records", "retire", "--app", "app-b", "--tenant", "tenant-other", "--entity", "entity-1", "--id", "record-9", "--json")
	assertFailure(t, got, exitRefused, "NOT_FOUND")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v to an app-tenant the entitlement doesn't reach", fake.mutations)
	}
}
