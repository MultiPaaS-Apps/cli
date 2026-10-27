package main

import (
	"maps"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestFeedbackSendInAProjectIsAboutItsApplicationAndPinnedVersion(t *testing.T) {
	fake := projectFake(t)
	fake.revisions["app-a-000"] = 2
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	pinned := readProjectForTest(t).APIVersion
	if pinned != "rev-2" {
		t.Fatalf("init pinned %q", pinned)
	}
	fake.mutations = nil

	got := fake.run(t, "feedback", "send", "--details", "Upsert drops a field", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"kind": "feature", "details": "Upsert drops a field", "apiVersion": pinned}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	wantPrinted := `{"feedback":{"id":"feedback-1","kind":"feature","status":"received","summary":null,"details":"Upsert drops a field",` +
		`"source":"cli","cliVersion":"0.1.0","apiVersion":"rev-2","platformVersion":"1.0.0","application":{"id":"app-a-000","name":"App app-a-000"},` +
		`"client":{"id":"client-a","name":"Acme"},"sentFromRemovedScope":false,"createdAt":"2026-10-11T00:00:00.000Z","updatedAt":"2026-10-11T00:00:00.000Z"}}`
	if compact(got.stdout) != wantPrinted {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestFeedbackSendGeneralSendsAboutApplicationFalse(t *testing.T) {
	fake := projectFake(t)
	fake.revisions["app-a-000"] = 2
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	fake.mutations = nil

	got := fake.run(t, "feedback", "send", "--general", "--kind", "bug", "--summary", "S", "--details", "D", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"kind": "bug", "summary": "S", "details": "D", "aboutApplication": false}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "" {
		t.Fatalf("scope %q", last)
	}
}

func TestFeedbackSendOutsideAProjectSendsNoScope(t *testing.T) {
	fake := signedInFake(t, fresh)
	t.Chdir(t.TempDir())

	got := fake.run(t, "feedback", "send", "--details", "D", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"kind": "feature", "details": "D"}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "" {
		t.Fatalf("scope %q", last)
	}
}

func TestFeedbackSendForAnotherAppSendsNoPinnedVersion(t *testing.T) {
	fake := projectFake(t)
	fake.revisions["app-a-000"] = 2
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	fake.mutations = nil

	got := fake.run(t, "feedback", "send", "--app", "app-a-001", "--details", "D", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"kind": "feature", "details": "D"}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-001" {
		t.Fatalf("scope %q", last)
	}
}

func TestFeedbackSendReadsDetailsFromStdin(t *testing.T) {
	fake := projectFake(t)

	got := fake.runWithStdin(t, "line one\nline two\n", "feedback", "send", "--details-file", "-", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"kind": "feature", "details": "line one\nline two\n"}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
}

func TestFeedbackSendViaPluginMarksIt(t *testing.T) {
	fake := projectFake(t)

	got := fake.run(t, "feedback", "send", "--via", "plugin", "--details", "D", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"kind": "feature", "details": "D", "viaPlugin": true}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
}

func TestFeedbackSendValidationCarriesFieldErrors(t *testing.T) {
	fake := projectFake(t)

	got := fake.run(t, "feedback", "send", "--details", "too-long", "--json")
	assertFailure(t, got, exitRefused, "VALIDATION")
	var failure struct{ Error cliError }
	decode(t, got.stdout, &failure)
	want := []fieldError{{Field: "details", Rule: "maxLength", Message: "Details must be at most 10000 characters"}}
	if !reflect.DeepEqual(failure.Error.FieldErrors, want) {
		t.Fatalf("fieldErrors %v, want %v", failure.Error.FieldErrors, want)
	}
}

func TestFeedbackSendThrottledIsRateLimited(t *testing.T) {
	fake := projectFake(t)

	assertFailure(t, fake.run(t, "feedback", "send", "--details", "throttle", "--json"), exitRefused, "RATE_LIMITED")
}

func TestFeedbackSendRefusesAnUnknownKind(t *testing.T) {
	fake := projectFake(t)

	assertFailure(t, fake.run(t, "feedback", "send", "--kind", "limitation", "--details", "D", "--json"), exitRefused, "CLI_ERROR")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestFeedbackSendNeedsDetails(t *testing.T) {
	fake := projectFake(t)

	got := fake.run(t, "feedback", "send", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestFeedbackSendGeneralFilesEvenWhenTheProjectsApplicationIsGone(t *testing.T) {
	fake := projectFake(t)
	if err := os.WriteFile(projectFile, []byte(`{"applicationId":"app-gone","testAppTenantId":"tenant-gone","apiVersion":"rev-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := fake.run(t, "feedback", "send", "--general", "--details", "My app disappeared", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"kind": "feature", "details": "My app disappeared", "aboutApplication": false}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "" {
		t.Fatalf("scope %q", last)
	}
}

func TestFeedbackListShowsYourReportsNewestFirst(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "feedback", "send", "--details", "first", "--json")
	fake.run(t, "feedback", "send", "--details", "second", "--json")

	got := fake.run(t, "feedback", "list", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	var listed []map[string]any
	decode(t, got.stdout, &listed)
	if len(listed) != 2 || listed[0]["id"] != "feedback-2" || listed[1]["id"] != "feedback-1" {
		t.Fatalf("listed %s", got.stdout)
	}
	for _, report := range listed {
		if _, has := report["history"]; has {
			t.Fatalf("list carries history: %s", got.stdout)
		}
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "" {
		t.Fatalf("scope %q", last)
	}
}

func TestFeedbackListTitlesAReportWithNoSummaryByTheFirstLineOfItsDetails(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "feedback", "send", "--details", "Upsert drops a field\nsecond line", "--json")
	fake.run(t, "feedback", "send", "--summary", "  Explicit title  ", "--details", "other", "--json")

	got := fake.run(t, "feedback", "list")
	if got.exit != exitOK || !regexp.MustCompile(`(?m)^feedback-1\s+feature\s+received\s+\S+\s+Upsert drops a field$`).MatchString(got.stdout) ||
		!regexp.MustCompile(`(?m)^feedback-2\s+feature\s+received\s+\S+\s+Explicit title$`).MatchString(got.stdout) {
		t.Fatalf("exit %d:\n%s", got.exit, got.stdout)
	}
}

func TestFeedbackListWithNoReportsIsAnEmptyArray(t *testing.T) {
	fake := projectFake(t)

	got := fake.run(t, "feedback", "list", "--json")
	if got.exit != exitOK || compact(got.stdout) != "[]" {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestFeedbackShowIncludesHistory(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "feedback", "send", "--details", "D", "--json")

	got := fake.run(t, "feedback", "show", "--id", "feedback-1", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	var shown struct {
		Feedback struct {
			ID      string `json:"id"`
			History []struct{ Status string }
		}
	}
	decode(t, got.stdout, &shown)
	if shown.Feedback.ID != "feedback-1" || len(shown.Feedback.History) != 1 || shown.Feedback.History[0].Status != "received" {
		t.Fatalf("shown %s", got.stdout)
	}
}

func TestFeedbackShowSaysWhenTheScopeHasSinceBeenRemoved(t *testing.T) {
	fake := projectFake(t)
	fake.feedback = append(fake.feedback,
		seededFeedback("feedback-1", map[string]any{"sentFromRemovedScope": true, "client": map[string]any{"id": "client-a", "name": "Acme"}}),
		seededFeedback("feedback-2", nil),
		seededFeedback("feedback-3", map[string]any{"sentFromRemovedScope": true}))

	// The first report's application was removed; the third was filed from a client-only scope whose client was removed.
	for _, id := range []string{"feedback-1", "feedback-3"} {
		removed := fake.run(t, "feedback", "show", "--id", id)
		if removed.exit != exitOK || !strings.Contains(removed.stdout, "feature, received, sent from a scope that has since been removed\n") {
			t.Fatalf("%s: exit %d:\n%s", id, removed.exit, removed.stdout)
		}
	}
	never := fake.run(t, "feedback", "show", "--id", "feedback-2")
	if never.exit != exitOK || !strings.Contains(never.stdout, "feature, received, no application\n") {
		t.Fatalf("exit %d:\n%s", never.exit, never.stdout)
	}
}

func TestFeedbackShowNamesAClientOnAClientScopedReport(t *testing.T) {
	fake := projectFake(t)
	fake.feedback = append(fake.feedback, seededFeedback("feedback-1", map[string]any{"client": map[string]any{"id": "client-a", "name": "Acme"}}))

	human := fake.run(t, "feedback", "show", "--id", "feedback-1")
	if human.exit != exitOK || !strings.Contains(human.stdout, "feature, received, no application\nclient Acme\n") {
		t.Fatalf("exit %d:\n%s", human.exit, human.stdout)
	}
	got := fake.run(t, "feedback", "show", "--id", "feedback-1", "--json")
	var shown struct {
		Feedback struct{ Client struct{ ID, Name string } }
	}
	decode(t, got.stdout, &shown)
	if shown.Feedback.Client.ID != "client-a" || shown.Feedback.Client.Name != "Acme" {
		t.Fatalf("shown %s", got.stdout)
	}
}

func TestFeedbackShowUnknownIsNotFound(t *testing.T) {
	fake := projectFake(t)

	assertFailure(t, fake.run(t, "feedback", "show", "--id", "feedback-9", "--json"), exitRefused, "NOT_FOUND")
}

func TestFeedbackSettingsDefaultsOff(t *testing.T) {
	fake := projectFake(t)

	got := fake.run(t, "feedback", "settings", "--json")
	if got.exit != exitOK || compact(got.stdout) != `{"autoSend":false}` {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "" {
		t.Fatalf("scope %q", last)
	}
}

func TestFeedbackSettingsTurnsAutoSendOn(t *testing.T) {
	fake := projectFake(t)

	got := fake.run(t, "feedback", "settings", "--auto-send", "on", "--json")
	if got.exit != exitOK || compact(got.stdout) != `{"autoSend":true}` {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if want := []map[string]any{{"autoSend": true}}; !reflect.DeepEqual(fake.mutations, want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	read := fake.run(t, "feedback", "settings", "--json")
	if compact(read.stdout) != `{"autoSend":true}` {
		t.Fatalf("read back %s", read.stdout)
	}
	off := fake.run(t, "feedback", "settings", "--auto-send", "off")
	if off.exit != exitOK || off.stdout != "Auto-send is off\n" {
		t.Fatalf("exit %d: %q", off.exit, off.stdout)
	}
}

func TestFeedbackSettingsRefusesAnUnknownValue(t *testing.T) {
	fake := projectFake(t)

	assertFailure(t, fake.run(t, "feedback", "settings", "--auto-send", "maybe", "--json"), exitRefused, "CLI_ERROR")
	if len(fake.mutations) != 0 || len(fake.scopes) != 0 {
		t.Fatalf("sent %v on scopes %v", fake.mutations, fake.scopes)
	}
}

// seededFeedback is a report the fake already holds, as a web submission leaves it: no application or
// client, with overrides applied.
func seededFeedback(id string, overrides map[string]any) map[string]any {
	report := map[string]any{
		"id": id, "kind": "feature", "status": "received", "summary": nil, "details": "D", "source": "web", "cliVersion": nil, "apiVersion": nil,
		"platformVersion": nil, "application": nil, "client": nil, "sentFromRemovedScope": false,
		"createdAt": "2026-10-11T00:00:00.000Z", "updatedAt": "2026-10-11T00:00:00.000Z",
	}
	maps.Copy(report, overrides)
	return report
}
