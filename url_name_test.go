package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestURLNameShowsTheAppsNameAndHistory(t *testing.T) {
	fake := signedInFake(t, fresh)

	got := fake.run(t, "url-name", "show", "--app", "app-a-000", "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if compact(got.stdout) != `{"urlName":{"kind":"application","id":"app-a-000","name":"app-a-000","history":[{"name":"app-a-000","claimedAt":"2026-11-01T00:00:00.000Z","replacedAt":null}]}}` {
		t.Fatalf("stdout %s", got.stdout)
	}
}

func TestURLNameRenamesAnEnvironmentAndShowsItsSite(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.run(t, "envs", "create", "--app", "app-a-000", "--name", "test", "--json")

	got := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--env", "env-new-2", "--name", "qa")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if !strings.Contains(got.stdout, "environment env-new-2 url name \"qa\"\nsite https://qa--app-a-000--acme-co.example.test\n") {
		t.Fatalf("stdout %q", got.stdout)
	}
	if !regexp.MustCompile(`test\s+2026-11-01T00:00:00.000Z\s+2026-11-22T12:00:00.000Z`).MatchString(got.stdout) {
		t.Fatalf("history %q", got.stdout)
	}
	listed := fake.run(t, "envs", "list", "--app", "app-a-000")
	if !strings.Contains(listed.stdout, "SITE URL") || !strings.Contains(listed.stdout, "https://qa--app-a-000--acme-co.example.test") {
		t.Fatalf("list %s", listed.stdout)
	}
}

func TestURLNameTakingAnotherAppsOldNameNeedsConfirmTakeover(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.retiredAppNames["billing"] = map[string]any{"id": "app-a-001", "name": "App app-a-001"}

	refused := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--name", "billing", "--json")
	confirmed := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--name", "billing", "--confirm-takeover", "--json")

	assertFailure(t, refused, exitRefused, "URL_NAME_TAKEOVER_UNCONFIRMED")
	var failure struct{ Error cliError }
	decode(t, refused.stdout, &failure)
	if failure.Error.PreviousOwner == nil || *failure.Error.PreviousOwner != (previousOwner{ID: "app-a-001", Name: "App app-a-001"}) {
		t.Fatalf("previousOwner %+v: %s", failure.Error.PreviousOwner, refused.stdout)
	}
	if confirmed.exit != exitOK {
		t.Fatalf("exit %d: %s", confirmed.exit, confirmed.stdout)
	}
	if last := fake.mutations[len(fake.mutations)-1]; last["confirmTakeover"] != true || last["applicationId"] != "app-a-000" {
		t.Fatalf("sent %v", last)
	}
}

func TestURLNameClientRenamesOnceThenSaysWhenItMayAgain(t *testing.T) {
	fake := signedInFake(t, fresh)

	first := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--client", "--name", "acme-studio", "--json")
	second := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--client", "--name", "acme-works", "--json")

	if compact(first.stdout) != `{"urlName":{"kind":"client","id":"client-a","name":"acme-studio","changeableAt":"2026-12-22T12:00:00.000Z","history":[{"name":"acme-studio","claimedAt":"2026-11-22T12:00:00.000Z","replacedAt":null},{"name":"acme-co","claimedAt":"2026-11-01T00:00:00.000Z","replacedAt":"2026-11-22T12:00:00.000Z"}]}}` {
		t.Fatalf("first %s", first.stdout)
	}
	assertFailure(t, second, exitRefused, "URL_NAME_RENAME_TOO_SOON")
	var failure struct{ Error cliError }
	decode(t, second.stdout, &failure)
	if failure.Error.ChangeableAt != "2026-12-22T12:00:00.000Z" {
		t.Fatalf("changeableAt %q", failure.Error.ChangeableAt)
	}
}

func TestURLNameShowFindsAnEnvironmentByItsIdInCapitals(t *testing.T) {
	fake := signedInFake(t, fresh)

	got := fake.run(t, "url-name", "show", "--app", "app-a-000", "--env", "ENV-DEFAULT", "--json")

	if got.exit != exitOK || !strings.Contains(compact(got.stdout), `"id":"env-default"`) {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestEnvsListSiteUrlNamesTheRequestsOwnClient(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.entitlements = append(fake.entitlements, entitlement{clientID: "client-c", clientName: "Gamma"})
	fake.apps["client-c"] = []fakeRow{{id: "app-c", name: "Ledger"}}
	fake.environments["app-c"] = []fakeEnv{{id: "env-c", name: "Default", isDefault: true}}

	got := fake.run(t, "envs", "list", "--app", "app-c", "--json")

	if got.exit != exitOK || !strings.Contains(compact(got.stdout), `"siteUrl":"https://app-c--client-c.example.test"`) {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
}

func TestURLNameAnInvalidNameCarriesItsRules(t *testing.T) {
	fake := signedInFake(t, fresh)

	asJSON := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--name", "Bad Name", "--json")
	human := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--name", "Bad Name")

	assertFailure(t, asJSON, exitRefused, "URL_NAME_INVALID")
	var failure struct{ Error cliError }
	decode(t, asJSON.stdout, &failure)
	if len(failure.Error.Rules) != 1 || failure.Error.Rules[0] != "pattern" {
		t.Fatalf("rules %v: %s", failure.Error.Rules, asJSON.stdout)
	}
	if !strings.Contains(human.stderr, "Rules the name breaks: pattern.") {
		t.Fatalf("stderr %q", human.stderr)
	}
}

func TestURLNameHumanRefusalsSayWhatToDoNext(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.retiredAppNames["billing"] = map[string]any{"id": "app-a-001", "name": "App app-a-001"}
	fake.run(t, "url-name", "rename", "--app", "app-a-000", "--client", "--name", "acme-studio")

	tooSoon := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--client", "--name", "acme-works")
	unconfirmed := fake.run(t, "url-name", "rename", "--app", "app-a-000", "--name", "billing")

	if !strings.Contains(tooSoon.stderr, "The name can change again at 2026-12-22T12:00:00.000Z.") {
		t.Fatalf("too soon stderr %q", tooSoon.stderr)
	}
	if !strings.Contains(unconfirmed.stderr, "(URL_NAME_TAKEOVER_UNCONFIRMED)\nRun the command again with --confirm-takeover to take the name.") {
		t.Fatalf("takeover stderr %q", unconfirmed.stderr)
	}
}

func TestURLNameShowUnknownEnvironmentIsNotFound(t *testing.T) {
	fake := signedInFake(t, fresh)

	assertFailure(t, fake.run(t, "url-name", "show", "--app", "app-a-000", "--env", "env-missing", "--json"), exitRefused, "NOT_FOUND")
}

func TestURLNameRefusesClientWithEnvOrConfirmTakeover(t *testing.T) {
	fake := signedInFake(t, fresh)

	for _, args := range [][]string{
		{"url-name", "show", "--app", "app-a-000", "--client", "--env", "env-default"},
		{"url-name", "rename", "--app", "app-a-000", "--client", "--confirm-takeover", "--name", "acme-works"},
	} {
		if got := fake.run(t, args...); got.exit == exitOK {
			t.Fatalf("%v: exit 0: %s", args, got.stdout)
		}
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}
