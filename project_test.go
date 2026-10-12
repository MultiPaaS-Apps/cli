package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestInitWritesTheProjectAndPinsTheNewestAPIVersion(t *testing.T) {
	fake := projectFake(t)
	fake.revisions["app-a-000"] = 3
	got := fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := projectConfig{ApplicationID: "app-a-000", TestAppTenantID: "tenant-a", APIVersion: "rev-3"}
	if saved := readProjectForTest(t); saved != want {
		t.Fatalf("multipaas.json %+v, want %+v", saved, want)
	}
	var printed struct {
		Config    projectConfig
		Endpoints endpoints
	}
	decode(t, got.stdout, &printed)
	wantEndpoints := endpoints{
		GraphQL:   fake.server.URL + "/app/app-a-000/graphql",
		Authorize: fake.server.URL + "/oauth/authorize",
		Token:     fake.server.URL + "/oauth/token",
		Revoke:    fake.server.URL + "/oauth/revoke",
	}
	if printed.Config != want || printed.Endpoints != wantEndpoints {
		t.Fatalf("printed %s", got.stdout)
	}
}

func TestInitRefusesAnAppTenantTheApplicationDoesNotHave(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-b", "--json")
	assertFailure(t, got, exitRefused, "NOT_FOUND")
	if _, err := os.Stat(projectFile); err == nil {
		t.Fatal("multipaas.json was written")
	}
}

func TestPinWritesOnlyAnAPIVersionTheApplicationHas(t *testing.T) {
	fake := projectFake(t)
	fake.revisions["app-a-000"] = 3
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	if got := fake.run(t, "pin", "--api-version", "rev-2", "--json"); got.exit != exitOK || readProjectForTest(t).APIVersion != "rev-2" {
		t.Fatalf("exit %d, pinned %q: %s", got.exit, readProjectForTest(t).APIVersion, got.stdout)
	}
	assertFailure(t, fake.run(t, "pin", "--api-version", "rev-9", "--json"), exitRefused, "NOT_FOUND")
	if pinned := readProjectForTest(t).APIVersion; pinned != "rev-2" {
		t.Fatalf("a refused pin changed the pin to %q", pinned)
	}
}

func TestPinWithoutAProjectIsRefusedNotInitialized(t *testing.T) {
	fake := projectFake(t)
	assertFailure(t, fake.run(t, "pin", "--json"), exitRefused, "NOT_INITIALIZED")
}

func TestCommandsTakeTheAppFromTheProjectAndRefuseWithoutOne(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "model", "show", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if len(fake.scopes) != 0 {
		t.Fatalf("sent %v without an app", fake.scopes)
	}
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	if got := fake.run(t, "model", "show", "--json"); got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
}

func TestVersionsListCollectsEveryPageAndMarksThePin(t *testing.T) {
	fake := projectFake(t)
	fake.revisions["app-a-000"] = 101
	pinned := `{"applicationId":"app-a-000","testAppTenantId":"tenant-a","apiVersion":"rev-1"}`
	if err := os.WriteFile(projectFile, []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	got := fake.run(t, "versions", "list", "--json")
	var listed []revision
	decode(t, got.stdout, &listed)
	pinnedIDs := []string{}
	for _, r := range listed {
		if r.Pinned {
			pinnedIDs = append(pinnedIDs, r.ID)
		}
	}
	if len(listed) != 101 || listed[0].ID != "rev-101" || !reflect.DeepEqual(pinnedIDs, []string{"rev-1"}) {
		t.Fatalf("listed %d, first %q, pinned %v", len(listed), listed[0].ID, pinnedIDs)
	}
}

func TestSchemaPullWritesThePinnedVersionsSchema(t *testing.T) {
	fake := projectFake(t)
	fake.revisions["app-a-000"] = 3
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--api-version", "rev-2", "--json")
	got := fake.run(t, "schema", "pull", "--json")
	if compact(got.stdout) != `{"apiVersion":"rev-2","path":"multipaas.graphql"}` {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	written, err := os.ReadFile("multipaas.graphql")
	if err != nil || !strings.Contains(string(written), "# rev-2\n") {
		t.Fatalf("multipaas.graphql %q (%v)", written, err)
	}
}

func TestSchemaPullWithoutAPinIsRefusedNotPinned(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	assertFailure(t, fake.run(t, "schema", "pull", "--json"), exitRefused, "NOT_PINNED")
}

func TestADefaultFromTheProjectIsUsedOnlyForTheProjectsApplication(t *testing.T) {
	fake := projectFake(t)
	project := `{"applicationId":"app-a-000","testAppTenantId":"tenant-a","apiVersion":"rev-1","oauthClientId":"oauth-client-1"}`
	if err := os.WriteFile(projectFile, []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	assertFailure(t, fake.run(t, "records", "list", "--app", "app-b", "--entity", "entity-1", "--json"), exitRefused, "CLI_ERROR")
	assertFailure(t, fake.run(t, "oauth-client", "update", "--app", "app-b", "--name", "X", "--json"), exitRefused, "CLI_ERROR")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
	for _, scope := range fake.scopes {
		if strings.Contains(scope, "app-b") {
			t.Fatalf("sent scope %q on another application's defaults", scope)
		}
	}
}

// projectFake is a signed-in fake, with the test in its own empty directory for multipaas.json.
func projectFake(t *testing.T) *fakeMultiPaaS {
	fake := signedInFake(t, fresh)
	t.Chdir(t.TempDir())
	return fake
}

func readProjectForTest(t *testing.T) projectConfig {
	t.Helper()
	var config projectConfig
	data, err := os.ReadFile(projectFile)
	if err == nil {
		err = json.Unmarshal(data, &config)
	}
	if err != nil {
		t.Fatal(err)
	}
	return config
}
