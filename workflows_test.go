package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const workflowDefinition = `{"key":"closeTicket","name":"Close ticket","description":"Closes a ticket","trigger":{"kind":"onDemand","inputs":[{"key":"ticketId","type":"ID","required":true}]},"runAs":{"kind":"starter"},"steps":[{"key":"find","name":"Find the ticket","description":"Looks the ticket up","kind":"find","entity":"Ticket"}]}`

func definitionFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWorkflowsCreateSendsTheDefinitionInTheApplicationScopeAndListShowsIt(t *testing.T) {
	fake := projectFake(t)
	created := fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", definitionFile(t, workflowDefinition), "--json")
	if created.exit != exitOK {
		t.Fatalf("exit %d: %s %s", created.exit, created.stdout, created.stderr)
	}
	if len(fake.mutations) != 1 || fake.mutations[0]["definition"] == nil || len(fake.mutations[0]) != 1 {
		t.Fatalf("sent %v", fake.mutations)
	}
	if got := fake.mutations[0]["definition"].(map[string]any)["key"]; got != "closeTicket" {
		t.Fatalf("sent key %v", got)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	if !strings.Contains(compact(created.stdout), `"workflow":{"id":"workflow-1","key":"closeTicket","name":"Close ticket"`) {
		t.Fatalf("printed %s", created.stdout)
	}

	listed := fake.run(t, "workflows", "list", "--app", "app-a-000")
	for _, want := range []string{"KEY", "TRIGGER", "STEPS", "ID", "closeTicket", "Close ticket", "onDemand", "1", "workflow-1"} {
		if !strings.Contains(listed.stdout, want) {
			t.Fatalf("workflows list lacks %q: %s", want, listed.stdout)
		}
	}
	if last := fake.workflowReads[len(fake.workflowReads)-1]; last["scope"] != "client-a/app-a-000" {
		t.Fatalf("list read %v", last)
	}
}

func TestWorkflowsCreateRefusedPrintsEachProblemAndKeepsThemInJSON(t *testing.T) {
	fake := projectFake(t)
	file := definitionFile(t, `{"key":"empty","name":"Empty","description":"","trigger":{"kind":"onDemand","inputs":[]},"runAs":{"kind":"starter"},"steps":[]}`)
	human := fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", file)
	if human.exit == exitOK || !strings.Contains(human.stderr, "WORKFLOW_INVALID") || !strings.Contains(human.stderr, "\nsteps: Too small: expected array to have >=1 items") {
		t.Fatalf("exit %d: %s", human.exit, human.stderr)
	}
	machine := fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", file, "--json")
	var failure struct {
		Error cliError `json:"error"`
	}
	decode(t, machine.stdout, &failure)
	want := []problem{{Path: "steps", Message: "Too small: expected array to have >=1 items"}}
	if failure.Error.Code != "WORKFLOW_INVALID" || !reflect.DeepEqual(failure.Error.Problems, want) {
		t.Fatalf("error %+v", failure.Error)
	}
}

func TestWorkflowsShowPrintsTheTriggerAndEachStep(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", definitionFile(t, workflowDefinition), "--json")
	got := fake.run(t, "workflows", "show", "closeTicket", "--app", "app-a-000")
	for _, want := range []string{"Close ticket (closeTicket)", "Closes a ticket", "Trigger: onDemand with inputs ticketId (ID, required)", "ticketId", "Runs as the caller", "1. Find the ticket — Looks the ticket up"} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("show lacks %q: %s", want, got.stdout)
		}
	}
	asJSON := fake.run(t, "workflows", "show", "closeTicket", "--app", "app-a-000", "--json")
	if !strings.Contains(compact(asJSON.stdout), `"workflow":{"id":"workflow-1","key":"closeTicket"`) {
		t.Fatalf("printed %s", asJSON.stdout)
	}
	if last := fake.workflowReads[len(fake.workflowReads)-1]; last["scope"] != "client-a/app-a-000" || len(last) != 1 {
		t.Fatalf("show read %v", last)
	}

	missing := fake.run(t, "workflows", "show", "nope", "--app", "app-a-000", "--json")
	var failure struct {
		Error cliError `json:"error"`
	}
	decode(t, missing.stdout, &failure)
	if missing.exit != exitRefused || failure.Error.Code != "NOT_FOUND" || failure.Error.Message != "no workflow nope" {
		t.Fatalf("exit %d: %s", missing.exit, missing.stdout)
	}
}

func TestWorkflowsUpdateReplacesTheDefinitionByTheStableId(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", definitionFile(t, workflowDefinition), "--json")
	fake.mutations = nil
	renamed := strings.Replace(workflowDefinition, `"name":"Close ticket"`, `"name":"Close a ticket"`, 1)
	got := fake.run(t, "workflows", "update", "closeTicket", "--app", "app-a-000", "--file", definitionFile(t, renamed), "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if len(fake.mutations) != 1 || fake.mutations[0]["id"] != "workflow-1" || fake.mutations[0]["definition"].(map[string]any)["name"] != "Close a ticket" {
		t.Fatalf("sent %v", fake.mutations)
	}
	if !strings.Contains(compact(got.stdout), `"id":"workflow-1","key":"closeTicket","name":"Close a ticket"`) {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestWorkflowsRunsListsEveryPageInTheEnvironmentWithTheFailedStepAndError(t *testing.T) {
	fake := projectFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})
	fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", definitionFile(t, workflowDefinition), "--json")
	run := func(id, status, createdAt string, records int, attempts []map[string]any) map[string]any {
		return map[string]any{"workflowId": "workflow-1", "id": id, "status": status, "triggerKind": "onDemand", "trigger": map[string]any{"inputs": map[string]any{}},
			"runAs": map[string]any{"id": "user-1", "email": "dev@example.com", "name": nil}, "attempts": attempts,
			"recordsChanged": records, "createdAt": createdAt, "finishedAt": createdAt}
	}
	fake.workflowRuns = []map[string]any{
		run("run-0", "FAILED", "2026-11-26T10:00:00Z", 0, []map[string]any{{"attempt": 1, "outcome": "failed_terminal", "failedStepKey": "find", "error": map[string]any{"code": "WORKFLOW_RAN_OUT_OF_TIME", "message": "ran out of time"}, "startedAt": "2026-11-26T10:00:00Z", "finishedAt": nil}}),
	}
	for i := 1; i <= maxPageSize; i++ {
		fake.workflowRuns = append(fake.workflowRuns, run(fmt.Sprintf("run-%d", i), "SUCCEEDED", "2026-11-25T10:00:00Z", 3, []map[string]any{{"attempt": 1, "outcome": "succeeded", "failedStepKey": nil, "error": nil, "startedAt": "2026-11-25T10:00:00Z", "finishedAt": nil}}))
	}

	got := fake.run(t, "workflows", "runs", "closeTicket", "--app", "app-a-000", "--env", "env-two")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	for _, want := range []string{"WHEN", "FAILED STEP", "FAILED", "dev@example.com", "find", "ran out of time", "SUCCEEDED"} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("runs lacks %q: %s", want, got.stdout)
		}
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "env:env-two" {
		t.Fatalf("scope %q", last)
	}
	sent := fake.workflowReads[len(fake.workflowReads)-1]
	if sent["workflowId"] != "workflow-1" || !reflect.DeepEqual(sent["page"], map[string]any{"page": float64(1), "pageSize": float64(maxPageSize)}) {
		t.Fatalf("runs sent %v", sent)
	}
	var parsed struct{ Runs []workflowRun }
	decode(t, fake.run(t, "workflows", "runs", "closeTicket", "--app", "app-a-000", "--env", "env-two", "--json").stdout, &parsed)
	if len(parsed.Runs) != maxPageSize+1 || parsed.Runs[0].ID != "run-0" || parsed.Runs[0].FailedStep != "find" || parsed.Runs[0].Error != "ran out of time" || parsed.Runs[1].RecordsChanged != 3 {
		t.Fatalf("runs %d, first %+v", len(parsed.Runs), parsed.Runs[0])
	}
}

func TestWorkflowsRunsShowsUnknownUserForARunWhoseUserIsGoneAndPassesTheNullThrough(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", definitionFile(t, workflowDefinition), "--json")
	fake.workflowRuns = []map[string]any{
		{"workflowId": "workflow-1", "id": "run-gone", "status": "SUCCEEDED", "triggerKind": "onDemand", "trigger": map[string]any{"inputs": map[string]any{}},
			"runAs": nil, "attempts": []map[string]any{}, "recordsChanged": 2, "createdAt": "2026-11-26T10:00:00Z", "finishedAt": "2026-11-26T10:00:01Z"},
		{"workflowId": "workflow-1", "id": "run-known", "status": "SUCCEEDED", "triggerKind": "onDemand", "trigger": map[string]any{"inputs": map[string]any{}},
			"runAs": map[string]any{"id": "user-1", "email": "dev@example.com", "name": nil}, "attempts": []map[string]any{}, "recordsChanged": 1, "createdAt": "2026-11-25T10:00:00Z", "finishedAt": "2026-11-25T10:00:01Z"},
	}

	human := fake.run(t, "workflows", "runs", "closeTicket", "--app", "app-a-000")
	lines := strings.Split(strings.TrimSpace(human.stdout), "\n")
	if human.exit != exitOK || len(lines) != 3 || !strings.Contains(lines[1], "unknown user") || !strings.Contains(lines[2], "dev@example.com") || strings.Contains(lines[2], "unknown user") {
		t.Fatalf("exit %d: %s", human.exit, human.stdout)
	}

	var parsed struct {
		Runs []struct {
			ID    string
			RunAs *runUser
		}
	}
	asJSON := fake.run(t, "workflows", "runs", "closeTicket", "--app", "app-a-000", "--json")
	decode(t, asJSON.stdout, &parsed)
	if len(parsed.Runs) != 2 || parsed.Runs[0].ID != "run-gone" || parsed.Runs[0].RunAs != nil || parsed.Runs[1].RunAs == nil || parsed.Runs[1].RunAs.Email != "dev@example.com" {
		t.Fatalf("printed %s", asJSON.stdout)
	}
	if !strings.Contains(compact(asJSON.stdout), `"id":"run-gone"`) || !strings.Contains(compact(asJSON.stdout), `"runAs":null`) {
		t.Fatalf("the null is not passed through: %s", asJSON.stdout)
	}
}

func TestWorkflowsRunsDefaultsToTheEnvironmentInMultipaasJSON(t *testing.T) {
	fake := projectFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})
	fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", definitionFile(t, workflowDefinition), "--json")
	project := `{"applicationId":"app-a-000","testAppTenantId":"tenant-a","environmentId":"env-two","apiVersion":"rev-1"}`
	if err := os.WriteFile(projectFile, []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}

	got := fake.run(t, "workflows", "runs", "closeTicket", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "env:env-two" {
		t.Fatalf("scope %q, want the project's environment", last)
	}
}

func TestWorkflowsDeleteRemovesItFromTheList(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "workflows", "create", "--app", "app-a-000", "--file", definitionFile(t, workflowDefinition), "--json")
	fake.mutations = nil
	got := fake.run(t, "workflows", "delete", "closeTicket", "--app", "app-a-000", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if want := []map[string]any{{"id": "workflow-1"}}; !reflect.DeepEqual(fake.mutations, want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if listed := fake.run(t, "workflows", "list", "--app", "app-a-000", "--json"); compact(listed.stdout) != `{"workflows":[]}` {
		t.Fatalf("listed %s", listed.stdout)
	}
}
