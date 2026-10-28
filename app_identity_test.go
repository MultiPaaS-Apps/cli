package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestAppIdentityCreateShowsTheSecretOnceInHumanAndJSONOutput(t *testing.T) {
	fake := projectFake(t)
	human := fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync")
	if human.exit != exitOK {
		t.Fatalf("exit %d: %s", human.exit, human.stdout)
	}
	want := "Client ID: app-identity-1\nSecret:    mps_plaintext_1\n\nStore the secret now; it is not shown again.\n"
	if human.stdout != want {
		t.Fatalf("printed %q, want %q", human.stdout, want)
	}

	got := fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "reporting", "--json")
	var created struct {
		AppIdentity struct{ Id, Name string }
		Secret      string
	}
	decode(t, got.stdout, &created)
	if created.AppIdentity.Id != "app-identity-2" || created.AppIdentity.Name != "reporting" || created.Secret != "mps_plaintext_2" {
		t.Fatalf("created %+v", created)
	}
	wantSent := []map[string]any{{"input": map[string]any{"name": "nightly sync"}}, {"input": map[string]any{"name": "reporting"}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}

	listed := fake.run(t, "app-identity", "list", "--app", "app-a-000", "--json")
	if strings.Contains(listed.stdout, "mps_plaintext") {
		t.Fatalf("list shows a secret: %s", listed.stdout)
	}
}

func TestAppIdentitySecretAddRevokeAndListAndRetire(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")

	added := fake.run(t, "app-identity", "secret", "add", "app-identity-1", "--app", "app-a-000")
	if want := "Client ID: app-identity-1\nSecret:    mps_plaintext_2\n\nStore the secret now; it is not shown again.\n"; added.stdout != want {
		t.Fatalf("printed %q, want %q", added.stdout, want)
	}

	listed := fake.run(t, "app-identity", "secret", "list", "app-identity-1", "--app", "app-a-000")
	for _, want := range []string{"ID", "LAST FOUR", "CREATED", "LAST USED", "secret-1", "0001", "secret-2", "0002"} {
		if !strings.Contains(listed.stdout, want) {
			t.Fatalf("secret list lacks %q: %s", want, listed.stdout)
		}
	}

	revoked := fake.run(t, "app-identity", "secret", "revoke", "secret-1", "--app", "app-a-000")
	if want := "Revoked secret ••••0001\n"; revoked.stdout != want {
		t.Fatalf("printed %q, want %q", revoked.stdout, want)
	}
	if got := fake.mutations[len(fake.mutations)-1]; !reflect.DeepEqual(got, map[string]any{"id": "secret-1"}) {
		t.Fatalf("revoke sent %v", got)
	}
	if secrets := fake.appIdentities[0]["secrets"].([]map[string]any); len(secrets) != 1 || secrets[0]["id"] != "secret-2" {
		t.Fatalf("secrets left %v", secrets)
	}

	retired := fake.run(t, "app-identity", "retire", "app-identity-1", "--app", "app-a-000")
	if want := "Retired nightly sync; its tokens stop working now.\n"; retired.stdout != want {
		t.Fatalf("printed %q, want %q", retired.stdout, want)
	}
	if got := fake.mutations[len(fake.mutations)-1]; !reflect.DeepEqual(got, map[string]any{"id": "app-identity-1"}) {
		t.Fatalf("retire sent %v", got)
	}
	if len(fake.appIdentities) != 0 {
		t.Fatalf("identities left %v", fake.appIdentities)
	}
}

func TestAppIdentitySecretIDsAreNeverReusedAfterARevoke(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
	fake.run(t, "app-identity", "secret", "add", "app-identity-1", "--app", "app-a-000", "--json")
	fake.run(t, "app-identity", "secret", "revoke", "secret-1", "--app", "app-a-000", "--json")
	fake.run(t, "app-identity", "secret", "add", "app-identity-1", "--app", "app-a-000", "--json")

	got := fake.run(t, "app-identity", "secret", "list", "app-identity-1", "--app", "app-a-000", "--json")
	var listed struct{ Secrets []struct{ Id string } }
	decode(t, got.stdout, &listed)
	if len(listed.Secrets) != 2 || listed.Secrets[0].Id != "secret-2" || listed.Secrets[1].Id != "secret-3" {
		t.Fatalf("secrets %s, want secret-2 and secret-3", got.stdout)
	}

	fake.run(t, "app-identity", "secret", "revoke", "secret-3", "--app", "app-a-000", "--json")
	got = fake.run(t, "app-identity", "secret", "list", "app-identity-1", "--app", "app-a-000", "--json")
	decode(t, got.stdout, &listed)
	if len(listed.Secrets) != 1 || listed.Secrets[0].Id != "secret-2" {
		t.Fatalf("after revoking secret-3 the secrets are %s, want only secret-2", got.stdout)
	}

	fake.run(t, "app-identity", "retire", "app-identity-1", "--app", "app-a-000", "--json")
	next := fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "reporting", "--json")
	var created struct{ AppIdentity struct{ Id string } }
	decode(t, next.stdout, &created)
	if created.AppIdentity.Id != "app-identity-2" {
		t.Fatalf("an identity created after a retire got id %q, want app-identity-2", created.AppIdentity.Id)
	}
}

func TestAppIdentityListShowsNameClientIDSecretCountAndCreated(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
	got := fake.run(t, "app-identity", "list", "--app", "app-a-000")
	for _, want := range []string{"NAME", "CLIENT ID", "SECRETS", "CREATED", "nightly sync", "app-identity-1"} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("list lacks %q: %s", want, got.stdout)
		}
	}
	asJSON := fake.run(t, "app-identity", "list", "--app", "app-a-000", "--json")
	var parsed struct{ AppIdentities []struct{ Id string } }
	decode(t, asJSON.stdout, &parsed)
	if len(parsed.AppIdentities) != 1 || parsed.AppIdentities[0].Id != "app-identity-1" {
		t.Fatalf("listed %s", asJSON.stdout)
	}
}

func TestAppIdentityJSONPrintsNullForAnIdentityWithoutAUserAndASecretNeverUsed(t *testing.T) {
	fake := projectFake(t)
	fake.appIdentities = []map[string]any{{"id": "app-identity-1", "name": "nightly sync", "user": nil, "createdAt": "x",
		"secrets": []map[string]any{{"id": "secret-1", "lastFour": "0001", "createdAt": "x", "lastUsedAt": nil}}}}

	listed := fake.run(t, "app-identity", "list", "--app", "app-a-000", "--json")
	var identities struct {
		AppIdentities []map[string]any
	}
	decode(t, listed.stdout, &identities)
	if user, present := identities.AppIdentities[0]["user"]; !present || user != nil {
		t.Fatalf("user is %v (present %v) in %s", user, present, listed.stdout)
	}

	secrets := fake.run(t, "app-identity", "secret", "list", "app-identity-1", "--app", "app-a-000", "--json")
	var parsed struct {
		Secrets []map[string]any
	}
	decode(t, secrets.stdout, &parsed)
	if used, present := parsed.Secrets[0]["lastUsedAt"]; !present || used != nil {
		t.Fatalf("lastUsedAt is %v (present %v) in %s", used, present, secrets.stdout)
	}

	human := fake.run(t, "app-identity", "secret", "list", "app-identity-1", "--app", "app-a-000")
	if want := "ID        LAST FOUR  CREATED  LAST USED\nsecret-1  0001       x        -\n"; human.stdout != want {
		t.Fatalf("a never-used secret is not shown as - : %q", human.stdout)
	}
}

func TestAppIdentityCommandsAreRefusedUnauthorizedWithoutManageOAuth(t *testing.T) {
	fake := projectFake(t)
	fake.noManageOAuth = true
	got := fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
	assertFailure(t, got, exitRefused, "UNAUTHORIZED")
	got = fake.run(t, "app-identity", "list", "--app", "app-a-000", "--json")
	assertFailure(t, got, exitRefused, "UNAUTHORIZED")
}

func TestAppIdentityOperationsOnAnUnknownIDAreNotFound(t *testing.T) {
	fake := projectFake(t)
	for _, args := range [][]string{
		{"secret", "list", "app-identity-9"},
		{"secret", "add", "app-identity-9"},
		{"secret", "revoke", "secret-9"},
		{"retire", "app-identity-9"},
	} {
		got := fake.run(t, append(append([]string{"app-identity"}, args...), "--app", "app-a-000", "--json")...)
		assertFailure(t, got, exitRefused, "NOT_FOUND")
	}
	for _, sent := range fake.mutations {
		if sent["id"] == "secret-9" {
			t.Fatalf("revoke sent %v for a secret no identity has", sent)
		}
	}
}
