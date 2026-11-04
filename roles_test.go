package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRolesCreateSendsPermissionsInTheApplicationScopeAndListShowsThem(t *testing.T) {
	fake := projectFake(t)
	created := fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job",
		"--permission", "record.view_all", "--permission", "record.app_wide.create=entity-1", "--json")
	if created.exit != exitOK {
		t.Fatalf("exit %d: %s", created.exit, created.stdout)
	}
	wantSent := []map[string]any{{"input": map[string]any{"key": "sync", "name": "Sync job", "permissions": []any{
		map[string]any{"permission": "record.view_all"},
		map[string]any{"permission": "record.app_wide.create", "entityId": "entity-1"},
	}}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	if want := `{"role":{"id":"role-1","key":"sync","name":"Sync job","appTenant":null,"permissions":[{"permission":"record.view_all","entityId":null},{"permission":"record.app_wide.create","entityId":"entity-1"}]}}`; compact(created.stdout) != want {
		t.Fatalf("printed %s, want %s", compact(created.stdout), want)
	}

	listed := fake.run(t, "roles", "list", "--app", "app-a-000")
	for _, want := range []string{"KEY", "PERMISSIONS", "sync", "Sync job", "record.view_all,record.app_wide.create=entity-1", "role-1"} {
		if !strings.Contains(listed.stdout, want) {
			t.Fatalf("roles list lacks %q: %s", want, listed.stdout)
		}
	}
}

func TestRolesCreateRefusesAPermissionWithNoName(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--permission", "=entity-1")
	if got.exit == exitOK || len(fake.mutations) != 0 {
		t.Fatalf("exit %d, sent %v: %s", got.exit, fake.mutations, got.stdout)
	}
}

func TestRolesUpdateAddsAndRemovesPermissionsInTheApplicationScope(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "member", "--name", "Member",
		"--permission", "record.create", "--permission", "record.view_all=entity-1", "--json")
	fake.mutations = nil
	got := fake.run(t, "roles", "update", "--app", "app-a-000", "--role", "member",
		"--add-permission", "record.edit_all=entity-1", "--add-permission", "record.create",
		"--remove-permission", "record.view_all=entity-1", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	wantSent := []map[string]any{{"input": map[string]any{"id": "role-1", "name": nil, "permissions": []any{
		map[string]any{"permission": "record.create"},
		map[string]any{"permission": "record.edit_all", "entityId": "entity-1"},
	}}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	if want := `{"role":{"id":"role-1","key":"member","name":"Member","appTenant":null,"permissions":[{"permission":"record.create","entityId":null},{"permission":"record.edit_all","entityId":"entity-1"}]}}`; compact(got.stdout) != want {
		t.Fatalf("printed %s, want %s", compact(got.stdout), want)
	}
	listed := fake.run(t, "roles", "list", "--app", "app-a-000")
	if !strings.Contains(listed.stdout, "record.create,record.edit_all=entity-1") {
		t.Fatalf("roles list: %s", listed.stdout)
	}
}

func TestRolesUpdateRemovingTheLastPermissionSendsAnEmptySet(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--permission", "record.view_all", "--json")
	fake.mutations = nil
	got := fake.run(t, "roles", "update", "--app", "app-a-000", "--role", "sync", "--remove-permission", "record.view_all")
	if want := "Updated role sync (role-1)\n"; got.exit != exitOK || got.stdout != want {
		t.Fatalf("exit %d, printed %q, want %q", got.exit, got.stdout, want)
	}
	wantSent := []map[string]any{{"input": map[string]any{"id": "role-1", "name": nil, "permissions": []any{}}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
}

func TestRolesUpdateNameOnlyLeavesThePermissions(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--permission", "record.view_all", "--json")
	fake.mutations = nil
	got := fake.run(t, "roles", "update", "--app", "app-a-000", "--role", "role-1", "--name", "Nightly sync", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	wantSent := []map[string]any{{"input": map[string]any{"id": "role-1", "name": "Nightly sync", "permissions": nil}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
	if want := `{"role":{"id":"role-1","key":"sync","name":"Nightly sync","appTenant":null,"permissions":[{"permission":"record.view_all","entityId":null}]}}`; compact(got.stdout) != want {
		t.Fatalf("printed %s, want %s", compact(got.stdout), want)
	}
}

func TestRolesUpdateMatchesEntityIdsWithoutCase(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "reader", "--name", "Reader", "--permission", "record.view_all=entity-a", "--json")
	fake.mutations = nil
	got := fake.run(t, "roles", "update", "--app", "app-a-000", "--role", "reader",
		"--add-permission", "record.view_all=ENTITY-A", "--add-permission", "record.create", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	wantSent := []map[string]any{{"input": map[string]any{"id": "role-1", "name": nil, "permissions": []any{
		map[string]any{"permission": "record.view_all", "entityId": "entity-a"},
		map[string]any{"permission": "record.create"},
	}}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
	removed := fake.run(t, "roles", "update", "--app", "app-a-000", "--role", "reader", "--remove-permission", "record.view_all=ENTITY-A")
	if removed.exit != exitOK {
		t.Fatalf("remove: exit %d: %s %s", removed.exit, removed.stdout, removed.stderr)
	}
}

func TestRolesUpdateRefusals(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "member", "--name", "Member", "--permission", "record.create", "--json")
	fake.roles = append(fake.roles, map[string]any{"id": "role-tenant", "key": "member", "name": "Tenant member", "appTenant": map[string]any{"id": "tenant-a"}, "permissions": []map[string]any{}})
	fake.mutations = nil
	for _, c := range []struct {
		name  string
		args  []string
		want  string
		exact string
	}{
		{"no change", []string{"--role", "member"}, "give --add-permission, --remove-permission or --name", ""},
		{"add names no permission", []string{"--role", "member", "--add-permission", "=entity-1"}, `--add-permission "=entity-1" names no permission`, ""},
		{"add names no entity id", []string{"--role", "member", "--add-permission", "record.view_all="}, `--add-permission "record.view_all=" names no entity id`, ""},
		{"remove names no entity id", []string{"--role", "member", "--remove-permission", "record.view_all="}, `--remove-permission "record.view_all=" names no entity id`, ""},
		{"remove names no permission", []string{"--role", "member", "--remove-permission", ""}, `--remove-permission "" names no permission`, ""},
		{"remove not carried", []string{"--role", "member", "--remove-permission", "record.view_all=entity-1"}, "role member doesn't carry record.view_all=entity-1", "multipaas: role member doesn't carry record.view_all=entity-1 (NOT_FOUND)\n"},
		{"unknown role", []string{"--role", "nope", "--add-permission", "record.view_all"}, "no role nope in application app-a-000", "multipaas: no role nope in application app-a-000 (NOT_FOUND)\n"},
		{"app-tenant role", []string{"--role", "role-tenant", "--add-permission", "record.view_all"}, "no role role-tenant in application app-a-000", "multipaas: no role role-tenant in application app-a-000 (NOT_FOUND)\n"},
		{"empty name", []string{"--role", "member", "--name", ""}, "--name is empty", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := fake.run(t, append([]string{"roles", "update", "--app", "app-a-000"}, c.args...)...)
			if got.exit == exitOK || !strings.Contains(got.stdout+got.stderr, c.want) || (c.exact != "" && got.stderr != c.exact) {
				t.Fatalf("exit %d, stdout %q, stderr %q, want %q (whole stderr %q)", got.exit, got.stdout, got.stderr, c.want, c.exact)
			}
		})
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestRolesCreateRefusesAPermissionWithNoEntityId(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--permission", "record.view_all=")
	if got.exit == exitOK || len(fake.mutations) != 0 || !strings.Contains(got.stderr, `--permission "record.view_all=" names no entity id`) {
		t.Fatalf("exit %d, sent %v, stderr %q", got.exit, fake.mutations, got.stderr)
	}
}

func TestRolesPermissionsListsTheCatalog(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "roles", "permissions", "--app", "app-a-000", "--json")
	want := `{"permissions":[{"key":"record.view_all","level":"appTenant","entityTargeted":true},{"key":"record.app_wide.create","level":"application","entityTargeted":true}]}`
	if compact(got.stdout) != want {
		t.Fatalf("printed %s, want %s", compact(got.stdout), want)
	}
}

func TestAppIdentityRoleAddListAndRemoveKeepAppTenantAndAppWideGrantsApart(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--json")
	fake.mutations = nil

	inTenant := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--role", "sync", "--app", "app-a-000", "--tenant", "tenant-a")
	if want := "Gave nightly sync the role sync in app-tenant tenant-a.\n"; inTenant.stdout != want {
		t.Fatalf("printed %q, want %q", inTenant.stdout, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
	appWide := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--role", "sync", "--app", "app-a-000", "--app-wide", "--json")
	if appWide.exit != exitOK {
		t.Fatalf("exit %d: %s", appWide.exit, appWide.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	wantSent := []map[string]any{{"roleId": "role-1", "userId": "service-user-1"}, {"roleId": "role-1", "userId": "service-user-1"}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}

	var listed struct{ Assignments []RoleAssignmentParts }
	decode(t, fake.run(t, "app-identity", "role", "list", "app-identity-1", "--app", "app-a-000", "--tenant", "tenant-a", "--json").stdout, &listed)
	if len(listed.Assignments) != 1 || listed.Assignments[0].Id != "assignment-1" {
		t.Fatalf("app-tenant list %+v", listed.Assignments)
	}
	decode(t, fake.run(t, "app-identity", "role", "list", "app-identity-1", "--app", "app-a-000", "--app-wide", "--json").stdout, &listed)
	if len(listed.Assignments) != 1 || listed.Assignments[0].Id != "assignment-2" {
		t.Fatalf("app-wide list %+v", listed.Assignments)
	}

	removed := fake.run(t, "app-identity", "role", "remove", "app-identity-1", "--role", "sync", "--app", "app-a-000", "--app-wide")
	if want := "Removed the role sync from nightly sync across the application.\n"; removed.stdout != want {
		t.Fatalf("printed %q, want %q", removed.stdout, want)
	}
	if got := fake.mutations[len(fake.mutations)-1]; !reflect.DeepEqual(got, map[string]any{"id": "assignment-2"}) {
		t.Fatalf("remove sent %v", got)
	}
	if len(fake.assignments) != 1 || fake.assignments[0]["id"] != "assignment-1" {
		t.Fatalf("assignments left %v", fake.assignments)
	}
}

func TestAppIdentityRoleAddWithEnvGrantsInThatEnvironment(t *testing.T) {
	fake := projectFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--env", "env-two", "--name", "staging sync", "--json")
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--json")

	got := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--app", "app-a-000", "--env", "env-two", "--app-wide", "--role", "sync", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "env:env-two" {
		t.Fatalf("scope %q", last)
	}
	assertFailure(t, fake.run(t, "app-identity", "role", "add", "app-identity-1", "--app", "app-a-000", "--app-wide", "--role", "sync", "--json"), exitRefused, "NOT_FOUND")
	assertFailure(t, fake.run(t, "app-identity", "role", "add", "app-identity-1", "--app", "app-a-000", "--env", "env-two", "--role", "sync", "--json"), exitRefused, "CLI_ERROR")
}

func TestAppIdentityRoleAddAtATenantOfAnotherEnvironmentFindsTheIdentityThere(t *testing.T) {
	fake := projectFake(t)
	fake.tenants["app-a-000"] = append(fake.tenants["app-a-000"], fakeRow{id: "tenant-o", name: "Other env", env: "env-other"})
	fake.appIdentities = []map[string]any{{"id": "app-identity-1", "name": "nightly sync", "environment": map[string]any{"id": "env-other"}, "user": map[string]any{"id": "service-user-1"},
		"createdAt": "x", "secrets": []map[string]any{}}}
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--json")

	got := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--app", "app-a-000", "--tenant", "tenant-o", "--role", "sync", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestAppIdentityRoleAddAtATenantCreatedInAnEnvironmentFindsTheIdentityThere(t *testing.T) {
	fake := projectFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})
	var created struct{ AppTenant struct{ Id string } }
	decode(t, fake.run(t, "tenants", "create", "--app", "app-a-000", "--env", "env-two", "--name", "Second", "--json").stdout, &created)
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--env", "env-two", "--name", "staging sync", "--json")
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--json")

	got := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--app", "app-a-000", "--tenant", created.AppTenant.Id, "--role", "sync", "--json")
	if created.AppTenant.Id == "" || got.exit != exitOK {
		t.Fatalf("tenant %q, exit %d: %s", created.AppTenant.Id, got.exit, got.stdout)
	}
}

func TestAppTenantsCreatedInTwoEnvironmentsHaveOwnIdsAndTheirOwnEnvironments(t *testing.T) {
	fake := projectFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"}, fakeEnv{id: "env-three", name: "Preview"})
	createdIn := func(env string) string {
		var created struct{ AppTenant struct{ Id string } }
		decode(t, fake.run(t, "tenants", "create", "--app", "app-a-000", "--env", env, "--name", env, "--json").stdout, &created)
		return created.AppTenant.Id
	}
	inTwo, inThree := createdIn("env-two"), createdIn("env-three")
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--env", "env-two", "--name", "staging sync", "--json")
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--json")

	if inTwo == "" || inTwo == inThree {
		t.Fatalf("app-tenants created in two environments got ids %q and %q", inTwo, inThree)
	}
	got := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--app", "app-a-000", "--tenant", inTwo, "--role", "sync", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestAppIdentityRoleAddAtATenantLooksTheIdentityUpInTheTenantScope(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "sync", "--name", "Sync job", "--json")
	fake.scopes = nil

	got := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--app", "app-a-000", "--tenant", "tenant-a", "--role", "sync", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if slices.Contains(fake.scopes, "client-a/app-a-000") || !slices.Contains(fake.scopes, "client-a/app-a-000/tenant-a") {
		t.Fatalf("the identity lookup left the app-tenant's scope: %v", fake.scopes)
	}
}

func TestAppIdentityRoleAddDefaultsToTheTestAppTenant(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "init", "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	fake.run(t, "app-identity", "create", "--name", "nightly sync", "--json")
	fake.run(t, "roles", "create", "--key", "sync", "--name", "Sync job", "--json")
	got := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--role", "sync", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
}

func TestAppIdentityRoleKeySharedByAnApplicationRoleAndAnAppTenantRoleIsAmbiguous(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
	fake.roles = []map[string]any{
		{"id": "role-app", "key": "sync", "name": "Sync job", "appTenant": nil, "permissions": []map[string]any{}},
		{"id": "role-tenant", "key": "sync", "name": "Tenant sync", "appTenant": map[string]any{"id": "tenant-a"}, "permissions": []map[string]any{}},
	}
	fake.mutations = nil
	add := func(role string) result {
		return fake.run(t, "app-identity", "role", "add", "app-identity-1", "--role", role, "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	}
	remove := func(role string) result {
		return fake.run(t, "app-identity", "role", "remove", "app-identity-1", "--role", role, "--app", "app-a-000", "--tenant", "tenant-a", "--json")
	}

	if got := add("sync"); got.exit == exitOK || !strings.Contains(got.stdout+got.stderr, "AMBIGUOUS_ROLE") || len(fake.mutations) != 0 {
		t.Fatalf("add by shared key: exit %d, sent %v, stdout %q, stderr %q", got.exit, fake.mutations, got.stdout, got.stderr)
	}
	if got := add("role-tenant"); got.exit != exitOK {
		t.Fatalf("add by id: exit %d: %s", got.exit, got.stdout)
	}
	if got := add("role-app"); got.exit != exitOK {
		t.Fatalf("add application role by id: exit %d: %s", got.exit, got.stdout)
	}
	wantSent := []map[string]any{{"roleId": "role-tenant", "userId": "service-user-1"}, {"roleId": "role-app", "userId": "service-user-1"}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}

	fake.mutations = nil
	if got := remove("sync"); got.exit == exitOK || !strings.Contains(got.stdout+got.stderr, "AMBIGUOUS_ROLE") || len(fake.mutations) != 0 {
		t.Fatalf("remove by shared key: exit %d, sent %v, stdout %q, stderr %q", got.exit, fake.mutations, got.stdout, got.stderr)
	}
	if got := remove("role-tenant"); got.exit != exitOK {
		t.Fatalf("remove by id: exit %d: %s", got.exit, got.stdout)
	}
	if want := []map[string]any{{"id": "assignment-1"}}; !reflect.DeepEqual(fake.mutations, want) {
		t.Fatalf("remove sent %v, want %v", fake.mutations, want)
	}
	if len(fake.assignments) != 1 || fake.assignments[0]["roleId"] != "role-app" {
		t.Fatalf("assignments left %v", fake.assignments)
	}
}

func TestAppIdentityRoleAmbiguousMessageLabelsEachIdByItsOwnScopeInEitherOrder(t *testing.T) {
	applicationRole := map[string]any{"id": "role-app", "key": "sync", "name": "Sync job", "appTenant": nil, "permissions": []map[string]any{}}
	tenantRole := map[string]any{"id": "role-tenant", "key": "sync", "name": "Tenant sync", "appTenant": map[string]any{"id": "tenant-a"}, "permissions": []map[string]any{}}
	for name, seeded := range map[string][]map[string]any{
		"application role first": {applicationRole, tenantRole},
		"app-tenant role first":  {tenantRole, applicationRole},
	} {
		t.Run(name, func(t *testing.T) {
			fake := projectFake(t)
			fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
			fake.roles = seeded
			got := fake.run(t, "app-identity", "role", "add", "app-identity-1", "--role", "sync", "--app", "app-a-000", "--tenant", "tenant-a")
			want := "role key sync names both the application role role-app and app-tenant tenant-a's role role-tenant; pass --role with the role's id"
			if got.exit == exitOK || !strings.Contains(got.stdout+got.stderr, want) {
				t.Fatalf("exit %d, stdout %q, stderr %q, want %q", got.exit, got.stdout, got.stderr, want)
			}
		})
	}
}

func TestRolesListWithTenantAddsThatAppTenantsOwnRoles(t *testing.T) {
	fake := projectFake(t)
	fake.roles = []map[string]any{
		{"id": "role-app", "key": "sync", "name": "Sync job", "appTenant": nil, "permissions": []map[string]any{}},
		{"id": "role-tenant", "key": "sync", "name": "Tenant sync", "appTenant": map[string]any{"id": "tenant-a"}, "permissions": []map[string]any{}},
	}
	ids := func(args ...string) []string {
		var listed struct{ Roles []RoleParts }
		decode(t, fake.run(t, append([]string{"roles", "list", "--app", "app-a-000", "--json"}, args...)...).stdout, &listed)
		got := []string{}
		for _, r := range listed.Roles {
			got = append(got, r.Id)
		}
		return got
	}
	if got := ids(); !reflect.DeepEqual(got, []string{"role-app"}) {
		t.Fatalf("roles list shows %v", got)
	}
	if got := ids("--tenant", "tenant-a"); !reflect.DeepEqual(got, []string{"role-app", "role-tenant"}) {
		t.Fatalf("roles list --tenant shows %v", got)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
}

func TestAppIdentityRoleRefusals(t *testing.T) {
	fake := projectFake(t)
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
	fake.mutations = nil
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown role", []string{"add", "app-identity-1", "--role", "nope", "--app", "app-a-000", "--app-wide"}, "no role nope in application app-a-000"},
		{"unknown identity", []string{"add", "app-identity-9", "--role", "nope", "--app", "app-a-000", "--app-wide"}, "no app identity app-identity-9"},
		{"remove not held", []string{"remove", "app-identity-1", "--role", "nope", "--app", "app-a-000", "--tenant", "tenant-a"}, "holds no role nope in app-tenant tenant-a, or you can't manage access there to see its roles"},
		{"tenant and app-wide", []string{"list", "app-identity-1", "--app", "app-a-000", "--tenant", "tenant-a", "--app-wide"}, "tenant"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := fake.run(t, append([]string{"app-identity", "role"}, c.args...)...)
			if got.exit == exitOK || !strings.Contains(got.stdout+got.stderr, c.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", got.exit, got.stdout, got.stderr)
			}
		})
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}
