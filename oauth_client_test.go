package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestOAuthClientCreateRegistersTheClientAndTheProjectRemembersIt(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	got := fake.run(t, "oauth-client", "create", "--name", "Web", "--redirect-uri", "https://app.example.com/callback",
		"--redirect-uri", "http://localhost:5173/callback", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"name": "Web", "redirectUris": []any{"https://app.example.com/callback", "http://localhost:5173/callback"}}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	if saved := readProjectForTest(t).OAuthClientID; saved != "oauth-client-1" {
		t.Fatalf("multipaas.json oauthClientId %q", saved)
	}
	fake.run(t, "init", "--app", "app-b", "--tenant", "tenant-b", "--json")
	if saved := readProjectForTest(t).OAuthClientID; saved != "" {
		t.Fatalf("init for another application kept oauthClientId %q", saved)
	}
}

func TestOAuthClientCreateNamesTheNewClientWhenItCannotSaveIt(t *testing.T) {
	fake := projectFake(t)
	if err := os.WriteFile(projectFile, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := fake.run(t, "oauth-client", "create", "--app", "app-a-000", "--name", "Web", "--redirect-uri", "https://app.example.com/callback", "--json")
	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "oauth-client-1") {
		t.Fatalf("the new client's id is not in %s", got.stdout)
	}
}

func TestOAuthClientUpdateSendsBackWhatWasNotGiven(t *testing.T) {
	fake := projectFake(t)
	fake.oauthClients = []map[string]any{{"id": "oauth-client-1", "name": "Web", "redirectUris": []any{"https://app.example.com/callback"}, "createdAt": "x"}}
	got := fake.run(t, "oauth-client", "update", "--app", "app-a-000", "--id", "oauth-client-1", "--name", "Web app", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	want := map[string]any{"input": map[string]any{"id": "oauth-client-1", "name": "Web app", "redirectUris": []any{"https://app.example.com/callback"}}}
	if !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations[0], want)
	}
}

func TestUpdatingAClientTheApplicationDoesNotHaveIsNotFoundAndSendsNothing(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "oauth-client", "update", "--app", "app-a-000", "--id", "oauth-client-9", "--name", "Web", "--json")
	assertFailure(t, got, exitRefused, "NOT_FOUND")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestWithoutManageOAuthTheClientCommandsAreRefusedUnauthorized(t *testing.T) {
	fake := projectFake(t)
	fake.noManageOAuth = true
	got := fake.run(t, "oauth-client", "create", "--app", "app-a-000", "--name", "Web", "--redirect-uri", "https://app.example.com/callback", "--json")
	assertFailure(t, got, exitRefused, "UNAUTHORIZED")
}
