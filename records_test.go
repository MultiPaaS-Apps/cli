package main

import (
	"encoding/json"
	"fmt"
	"maps"
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
	if compact(got.stdout) != `{"record":{"id":"record-1","appWide":false,"data":{"field-1":"Ship it"},"createdAt":"2026-10-11T00:00:00.000Z","updatedAt":"2026-10-11T00:00:00.000Z","version":"version-1","files":[]}}` {
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

func TestRecordsListWalksByCursorListingEachExistingRecordOnceAndNotOneCreatedBehindIt(t *testing.T) {
	fake := projectFake(t)
	for i := range 101 {
		fake.records["entity-1"] = append(fake.records["entity-1"], map[string]any{"id": fmt.Sprintf("record-%d", i), "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"})
	}
	added := false
	fake.afterRecordsPage = func(f *fakeMultiPaaS) {
		if !added {
			added = true
			f.records["entity-1"] = append([]map[string]any{{"id": "record-new", "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"}}, f.records["entity-1"]...)
		}
	}
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json")
	var records []RecordParts
	decode(t, got.stdout, &records)
	ids := map[string]bool{}
	for _, record := range records {
		if ids[record.Id] {
			t.Fatalf("listed %s twice", record.Id)
		}
		ids[record.Id] = true
	}
	if len(records) != 101 || ids["record-new"] {
		t.Fatalf("listed %d records, record-new listed: %v", len(records), ids["record-new"])
	}
	reads := fake.recordReads
	if len(reads) != 2 || reads[0].variables["cursor"] != nil || reads[1].variables["cursor"] != "after:record-99" {
		t.Fatalf("reads %v", reads)
	}
	if page := reads[1].variables["page"].(map[string]any); page["page"] != float64(0) {
		t.Fatalf("second read asked for page %v", page["page"])
	}
}

func TestRecordsListRefusesAServerThatSaysMoreFollowWithoutACursor(t *testing.T) {
	fake := projectFake(t)
	for i := range 101 {
		fake.records["entity-1"] = append(fake.records["entity-1"], map[string]any{"id": fmt.Sprintf("record-%d", i), "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"})
	}
	fake.omitNextCursor = true
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "sent no cursor") {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsListStopsWhenTheServerRepeatsACursor(t *testing.T) {
	for name, tc := range map[string]struct {
		cycle []string
		reads int
	}{
		"the same cursor twice": {cycle: []string{"same"}, reads: 2},
		"a cycle A, B, A":       {cycle: []string{"A", "B"}, reads: 3},
	} {
		t.Run(name, func(t *testing.T) {
			fake := projectFake(t)
			for i := range 101 {
				fake.records["entity-1"] = append(fake.records["entity-1"], map[string]any{"id": fmt.Sprintf("record-%d", i), "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"})
			}
			fake.cursorCycle = tc.cycle
			got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json")
			assertFailure(t, got, exitRefused, "CLI_ERROR")
			if !strings.Contains(got.stdout, "already sent") {
				t.Fatalf("printed %s", got.stdout)
			}
			if len(fake.recordReads) != tc.reads {
				t.Fatalf("read %d pages, want %d", len(fake.recordReads), tc.reads)
			}
		})
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
	fake.records["entity-1"] = []map[string]any{{"id": "record-1", "version": "v1", "data": map[string]any{"field-1": "a"}, "createdAt": "x", "updatedAt": "y"}}
	got := fake.run(t, "records", "get", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if compact(got.stdout) != `{"record":{"id":"record-1","appWide":false,"data":{"field-1":"a"},"createdAt":"x","updatedAt":"y","version":"v1","files":[]}}` {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsGetPrintsTheVersionWithoutJSON(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{{"id": "record-1", "version": "v7", "data": map[string]any{"field-1": "a"}, "createdAt": "x", "updatedAt": "y"}}
	got := fake.run(t, "records", "get", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1")
	if got.exit != exitOK || !strings.HasPrefix(got.stdout, "record record-1 version v7 ") {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
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

func TestRecordsScopeRefusesWhatTheServerRefuses(t *testing.T) {
	tenantRecord := map[string]any{"id": "mine", "appWide": false, "data": map[string]any{"field-1": "a"}, "createdAt": "x", "updatedAt": "x"}
	appWideRecord := map[string]any{"id": "shared", "appWide": true, "data": map[string]any{"field-1": "b"}, "createdAt": "x", "updatedAt": "x"}
	tenant, tenantHeader := []string{"--tenant", "tenant-a"}, "client-a/app-a-000/tenant-a"
	appWide, appWideHeader := []string{"--app-wide"}, "client-a/app-a-000"
	cases := []struct {
		name   string
		verb   string
		scope  []string
		header string
		target string
	}{
		{"app-wide get of a tenant record", "get", appWide, appWideHeader, "mine"},
		{"app-wide update of a tenant record", "update", appWide, appWideHeader, "mine"},
		{"app-wide retire of a tenant record", "retire", appWide, appWideHeader, "mine"},
		{"tenant update of an app-wide record", "update", tenant, tenantHeader, "shared"},
		{"tenant retire of an app-wide record", "retire", tenant, tenantHeader, "shared"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := projectFake(t)
			fake.records["entity-1"] = []map[string]any{maps.Clone(tenantRecord), maps.Clone(appWideRecord)}
			before := fmt.Sprint(fake.records["entity-1"])
			args := append([]string{"records", c.verb, "--app", "app-a-000"}, c.scope...)
			args = append(args, "--entity", "entity-1", "--id", c.target)
			if c.verb == "update" {
				args = append(args, "--data", `{"field-1":"changed"}`)
			}
			got := fake.run(t, append(args, "--json")...)
			assertFailure(t, got, exitRefused, "NOT_FOUND")
			if last := fake.scopes[len(fake.scopes)-1]; last != c.header {
				t.Fatalf("scope %q, want %q", last, c.header)
			}
			if after := fmt.Sprint(fake.records["entity-1"]); after != before {
				t.Fatalf("records changed from %s to %s", before, after)
			}
		})
	}
}

func TestRecordsScopeLetsATenantReadAnAppWideRecord(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{{"id": "shared", "version": "v1", "appWide": true, "data": map[string]any{"field-1": "b"}, "createdAt": "x", "updatedAt": "x"}}
	got := fake.run(t, "records", "get", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "shared", "--json")
	if got.exit != exitOK || compact(got.stdout) != `{"record":{"id":"shared","appWide":true,"data":{"field-1":"b"},"createdAt":"x","updatedAt":"x","version":"v1","files":[]}}` {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
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

func TestRecordsListSendsWhereAndOrderBy(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1",
		"--where", `{"f1": {"lte": 2}}`, "--order-by", "f1:desc", "--json")
	if got.exit != exitOK || len(fake.recordReads) != 1 {
		t.Fatalf("exit %d after %d reads: %s", got.exit, len(fake.recordReads), got.stdout)
	}
	sent := fake.recordReads[0].variables
	if !reflect.DeepEqual(sent["where"], map[string]any{"f1": map[string]any{"lte": 2.0}}) {
		t.Fatalf("sent where %v", sent["where"])
	}
	if !reflect.DeepEqual(sent["orderBy"], map[string]any{"field": "f1", "direction": "DESC"}) {
		t.Fatalf("sent orderBy %v", sent["orderBy"])
	}
	fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--order-by", "createdAt", "--json")
	if got := fake.recordReads[1].variables["orderBy"]; !reflect.DeepEqual(got, map[string]any{"field": "createdAt", "direction": "ASC"}) {
		t.Fatalf("sent orderBy %v without a direction", got)
	}
}

func TestRecordsListSendsNoWhereByDefault(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json")
	if got.exit != exitOK || len(fake.recordReads) != 1 {
		t.Fatalf("exit %d after %d reads: %s", got.exit, len(fake.recordReads), got.stdout)
	}
	sent := fake.recordReads[0].variables
	if sent["where"] != nil || sent["orderBy"] != nil {
		t.Fatalf("sent where %v, orderBy %v", sent["where"], sent["orderBy"])
	}
}

func TestRecordsListCountPrintsOnlyTheNumber(t *testing.T) {
	fake := projectFake(t)
	for i := range 3 {
		fake.records["entity-1"] = append(fake.records["entity-1"], map[string]any{"id": fmt.Sprintf("record-%d", i), "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"})
	}
	args := []string{"records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--count"}
	got := fake.run(t, args...)
	if got.exit != exitOK || got.stdout != "3\n" {
		t.Fatalf("exit %d, printed %q", got.exit, got.stdout)
	}
	if len(fake.recordReads) != 1 || fake.recordReads[0].operation != "RecordsCount" {
		t.Fatalf("sent %v", fake.recordReads)
	}
	if query := fake.recordReads[0].query; !strings.Contains(query, "totalCount") || strings.Contains(query, "items") {
		t.Fatalf("query %s", query)
	}
	got = fake.run(t, append(args, "--json")...)
	if compact(got.stdout) != `{"count":3}` {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsListCountSendsWhere(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1",
		"--where", `{"f1": {"lte": 2}}`, "--count")
	if got.exit != exitOK || len(fake.recordReads) != 1 || fake.recordReads[0].operation != "RecordsCount" {
		t.Fatalf("exit %d after reads %v: %s", got.exit, fake.recordReads, got.stdout)
	}
	if sent := fake.recordReads[0].variables["where"]; !reflect.DeepEqual(sent, map[string]any{"f1": map[string]any{"lte": 2.0}}) {
		t.Fatalf("count sent where %v", sent)
	}
}

func TestRecordsListAggregate(t *testing.T) {
	fake := projectFake(t)
	fake.aggregate = json.RawMessage(`{"f1":{"sum":14,"min":2,"max":5},"createdAt":{"min":"2026-01-01T00:00:00.000Z","max":"2026-02-01T00:00:00.000Z"}}`)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1",
		"--where", `{"f1": {"gte": 2}}`, "--aggregate", "f1", "--aggregate", "createdAt", "--json")
	if got.exit != exitOK || len(fake.recordReads) != 1 || fake.recordReads[0].operation != "RecordsAggregate" {
		t.Fatalf("exit %d after reads %v: %s", got.exit, fake.recordReads, got.stdout)
	}
	sent := fake.recordReads[0].variables
	if !reflect.DeepEqual(sent["fields"], []any{"f1", "createdAt"}) || sent["count"] != false ||
		!reflect.DeepEqual(sent["where"], map[string]any{"f1": map[string]any{"gte": 2.0}}) {
		t.Fatalf("sent %v", sent)
	}
	if query := fake.recordReads[0].query; strings.Contains(query, "items") {
		t.Fatalf("query %s", query)
	}
	if compact(got.stdout) != string(fake.aggregate) {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsListAggregateWithCount(t *testing.T) {
	fake := projectFake(t)
	for i := range 3 {
		fake.records["entity-1"] = append(fake.records["entity-1"], map[string]any{"id": fmt.Sprintf("record-%d", i), "data": map[string]any{}, "createdAt": "x", "updatedAt": "x"})
	}
	fake.aggregate = json.RawMessage(`{"f1":{"sum":6,"min":1,"max":3}}`)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1",
		"--aggregate", "f1", "--count", "--json")
	if got.exit != exitOK || len(fake.recordReads) != 1 || fake.recordReads[0].variables["count"] != true {
		t.Fatalf("exit %d after reads %v: %s", got.exit, fake.recordReads, got.stdout)
	}
	if compact(got.stdout) != `{"aggregate":{"f1":{"sum":6,"min":1,"max":3}},"count":3}` {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsListAggregateRefusesOrderBy(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1",
		"--aggregate", "f1", "--order-by", "createdAt", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "--aggregate takes no --order-by") || len(fake.recordReads) != 0 {
		t.Fatalf("printed %s after reads %v", got.stdout, fake.recordReads)
	}
}

func TestRecordsListAggregateRefusesEmpty(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1",
		"--aggregate", "", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "--aggregate is empty") || len(fake.recordReads) != 0 {
		t.Fatalf("printed %s after reads %v", got.stdout, fake.recordReads)
	}
}

func TestRecordsListRefusesBadFlags(t *testing.T) {
	fake := projectFake(t)
	for _, refused := range []struct {
		name    string
		flags   []string
		message string
	}{
		{"where_array", []string{"--where", `[1]`}, "--where must be a JSON object"},
		{"where_null", []string{"--where", `null`}, "--where must be a JSON object"},
		{"where_unterminated", []string{"--where", `{`}, "--where must be a JSON object"},
		{"order_by_unknown_direction", []string{"--order-by", "f1:sideways"}, "--order-by takes FIELD, FIELD:asc or FIELD:desc"},
		{"order_by_no_field", []string{"--order-by", ":desc"}, "--order-by needs a field id"},
		{"count_with_order_by", []string{"--count", "--order-by", "f1"}, "--count takes no --order-by"},
		{"where_empty", []string{"--where", ""}, "--where is empty"},
		{"order_by_empty", []string{"--order-by", ""}, "--order-by is empty"},
		{"count_with_empty_order_by", []string{"--count", "--order-by", ""}, "--order-by is empty"},
	} {
		t.Run(refused.name, func(t *testing.T) {
			got := fake.run(t, append([]string{"records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--json"}, refused.flags...)...)
			assertFailure(t, got, exitRefused, "CLI_ERROR")
			if !strings.Contains(got.stdout, refused.message) {
				t.Fatalf("%v printed %s, want %q", refused.flags, got.stdout, refused.message)
			}
		})
	}
	if len(fake.recordReads) != 0 {
		t.Fatalf("sent %v", fake.recordReads)
	}
}

func TestRecordsListShowsTheFieldAValidationRefusalNames(t *testing.T) {
	fake := projectFake(t)
	fake.refuseRecords = []map[string]any{{"field": "f1", "rule": "unknown", "message": "f1 is not a field of this entity"}}
	got := fake.run(t, "records", "list", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--where", `{"f1": {"eq": 1}}`, "--json")
	assertFailure(t, got, exitRefused, "VALIDATION")
	var failure struct{ Error cliError }
	decode(t, got.stdout, &failure)
	want := []fieldError{{Field: "f1", Rule: "unknown", Message: "f1 is not a field of this entity"}}
	if !reflect.DeepEqual(failure.Error.FieldErrors, want) {
		t.Fatalf("fieldErrors %v, want %v", failure.Error.FieldErrors, want)
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

func TestRecordsUpdateWithTheCurrentExpectedVersionWritesAndPrintsTheNewVersion(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{{"id": "record-1", "version": "v1", "data": map[string]any{"field-1": "a"}, "createdAt": "x", "updatedAt": "x"}}
	got := fake.run(t, "records", "update", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1",
		"--data", `{"field-1":"b"}`, "--expected-version", "v1", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if sent := fake.mutations[0]["input"].(map[string]any)["expectedVersion"]; sent != "v1" {
		t.Fatalf("sent expectedVersion %v", sent)
	}
	var printed struct{ Record struct{ Version string } }
	decode(t, got.stdout, &printed)
	if printed.Record.Version == "" || printed.Record.Version == "v1" {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestRecordsUpdateWithAStaleExpectedVersionIsRefusedWithConflictAndWritesNothing(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{{"id": "record-1", "version": "v2", "data": map[string]any{"field-1": "a"}, "createdAt": "x", "updatedAt": "x"}}
	got := fake.run(t, "records", "update", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1",
		"--data", `{"field-1":"b"}`, "--expected-version", "v1", "--json")
	assertFailure(t, got, exitRefused, "CONFLICT")
	if data := fake.records["entity-1"][0]["data"]; !reflect.DeepEqual(data, map[string]any{"field-1": "a"}) {
		t.Fatalf("record is now %v", data)
	}
}

func TestRecordsUpdateRefusesAnEmptyExpectedVersionAndWritesNothing(t *testing.T) {
	fake := projectFake(t)
	fake.records["entity-1"] = []map[string]any{{"id": "record-1", "version": "v1", "data": map[string]any{"field-1": "a"}, "createdAt": "x", "updatedAt": "x"}}
	got := fake.run(t, "records", "update", "--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-1",
		"--data", `{"field-1":"b"}`, "--expected-version", "", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "--expected-version is empty") {
		t.Fatalf("printed %s", got.stdout)
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}
