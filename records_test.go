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
	if compact(got.stdout) != `{"record":{"id":"record-1","appWide":false,"data":{"field-1":"Ship it"},"createdAt":"2026-10-11T00:00:00.000Z","updatedAt":"2026-10-11T00:00:00.000Z"}}` {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsListAppWideActsAtApplicationScope(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{
		{"id": "mine", "appWide": false, "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"},
		{"id": "shared", "appWide": true, "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"},
	}
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--app-wide", "--entity", "entity-1", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	var records []RecordParts
	decode(t, got.stdout, &records)
	if len(records) != 1 || records[0].Id != "shared" || !records[0].AppWide {
		t.Fatalf("listed %s", got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	if !reflect.DeepEqual(fake.recordScopes, []string{"APP_WIDE"}) {
		t.Fatalf("sent scopes %v", fake.recordScopes)
	}
}

func TestRecordsListWithoutAppWideSendsTenantScope(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{
		{"id": "mine", "appWide": false, "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"},
		{"id": "shared", "appWide": true, "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"},
	}
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json")
	var records []RecordParts
	decode(t, got.stdout, &records)
	if len(records) != 1 || records[0].Id != "mine" {
		t.Fatalf("listed %s", got.stdout)
	}
	if !reflect.DeepEqual(fake.recordScopes, []string{"TENANT"}) {
		t.Fatalf("sent scopes %v", fake.recordScopes)
	}
}

func TestRecordsCreateAppWideNeedsNoTenant(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "create", "--app", "app-a-000", "--app-wide", "--entity", "entity-1", "--data", `{"field-1":"hola"}`, "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	var created struct{ Record RecordParts }
	decode(t, got.stdout, &created)
	if !created.Record.AppWide {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsAppWideSkipsAnEntitlementConfinedToATenant(t *testing.T) {
	fake := projectFake(t)
	fake.entitlements = append([]entitlement{{clientID: "client-a", clientName: "Acme", applicationID: "app-a-000", appTenantID: "tenant-a"}}, fake.entitlements...)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--app-wide", "--entity", "entity-1", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
}

func TestRecordsAppWideRefusedWithOnlyTenantEntitlements(t *testing.T) {
	fake := projectFake(t)
	fake.entitlements = []entitlement{{clientID: "client-a", clientName: "Acme", applicationID: "app-a-000", appTenantID: "tenant-a"}}
	fake.recordScopes = nil
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--app-wide", "--entity", "entity-1", "--json")
	if got.exit == exitOK || !strings.Contains(got.stdout, "no application-wide access") {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if len(fake.recordScopes) != 0 {
		t.Fatalf("sent %v", fake.recordScopes)
	}
}

func TestRecordsTenantAndAppWideConflict(t *testing.T) {
	fake := projectFake(t)
	before := len(fake.scopes)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--app-wide", "--entity", "entity-1", "--json")
	if got.exit == exitOK || !strings.Contains(got.stdout, "none of the others can be") {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if len(fake.scopes) != before {
		t.Fatalf("sent %v", fake.scopes[before:])
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

func TestRecordsGetPrintsTheRecord(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{{"id": "record-1", "data": map[string]any{"field-1": "a"}, "createdAt": "x", "updatedAt": "y"}}
	got := fake.run(t, "records", "get", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if compact(got.stdout) != `{"record":{"id":"record-1","appWide":false,"data":{"field-1":"a"},"createdAt":"x","updatedAt":"y"}}` {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsGetSaysWhenThereIsNoSuchRecord(t *testing.T) {
	fake := projectFake(t)
	missing := fake.run(t, "records", "get", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-9", "--json")
	assertFailure(t, missing, exitRefused, "NOT_FOUND")
	if !strings.Contains(missing.stdout, "record-9") {
		t.Fatalf("printed %s", missing.stdout)
	}
}

func TestRecordsListByParentKeepsTheParentsChildren(t *testing.T) {
	fake := projectFake(t)
	for _, row := range []struct{ id, parent string }{{"child-1", "parent-1"}, {"child-2", "parent-2"}, {"child-3", "parent-1"}} {
		fake.records["entity-1"] = append(fake.records["entity-1"], map[string]any{"id": row.id, "data": map[string]any{"owner": row.parent}, "createdAt": "x", "updatedAt": "x"})
	}
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1",
		"--parent-field", "owner", "--parent-record", "parent-1", "--json")
	var records []RecordParts
	decode(t, got.stdout, &records)
	if len(records) != 2 || records[0].Id != "child-1" || records[1].Id != "child-3" {
		t.Fatalf("listed %s", got.stdout)
	}
}

func TestRecordsListByParentNeedsBothFlags(t *testing.T) {
	fake := projectFake(t)
	for _, flags := range [][]string{{"--parent-field", "owner"}, {"--parent-record", "parent-1"}} {
		seen := len(fake.scopes)
		alone := fake.run(t, append([]string{"records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json"}, flags...)...)
		assertFailure(t, alone, exitRefused, "CLI_ERROR")
		if !strings.Contains(alone.stdout, "--parent-field and --parent-record go together") || len(fake.scopes) != seen {
			t.Fatalf("printed %s after %d calls", alone.stdout, len(fake.scopes)-seen)
		}
	}
}
