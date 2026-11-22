package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestMemberInviteSendsTheEmailAndRoleToTheAppTenantAndPrintsIt(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "reviewer", "--name", "Reviewer", "--json")
	fake.mutations = nil

	got := fake.run(t, "member", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--role", "reviewer", "--email", "reviewer@example.com")

	if want := "Invited reviewer@example.com to reviewer in app-tenant tenant-a. They get the role when they accept the emailed link.\n"; got.stdout != want {
		t.Fatalf("printed %q, want %q", got.stdout, want)
	}
	wantSent := []map[string]any{{"input": map[string]any{"email": "reviewer@example.com", "groupIds": nil, "roleIds": []any{"role-1"}, "scope": "app_tenant"}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
}

func TestMemberInviteJSONNamesTheInvitation(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "reviewer", "--name", "Reviewer", "--json")

	got := fake.run(t, "member", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--role", "reviewer", "--email", "reviewer@example.com", "--json")

	if want := `{"invitation":{"id":"invitation-1","email":"reviewer@example.com"}}`; compact(got.stdout) != want {
		t.Fatalf("exit %d: %s, want %s", got.exit, got.stdout, want)
	}
}

func TestMemberInviteRefusals(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "reviewer", "--name", "Reviewer", "--json")
	fake.mutations = nil

	noTenant := fake.run(t, "member", "invite", "--app", "app-a-000", "--role", "reviewer", "--email", "reviewer@example.com", "--json")
	unknownRole := fake.run(t, "member", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--role", "nope", "--email", "reviewer@example.com", "--json")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v before the server refused anything", fake.mutations)
	}
	fake.noManageApp = true
	unauthorized := fake.run(t, "member", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--role", "reviewer", "--email", "reviewer@example.com", "--json")

	assertFailure(t, noTenant, exitRefused, "CLI_ERROR")
	if want := "--tenant is required: no multipaas.json here sets it for this application"; !strings.Contains(noTenant.stdout, want) {
		t.Fatalf("stdout %s does not say %q", noTenant.stdout, want)
	}
	assertFailure(t, unknownRole, exitRefused, "NOT_FOUND")
	assertFailure(t, unauthorized, exitRefused, "UNAUTHORIZED")
}
