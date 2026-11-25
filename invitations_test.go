package main

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// invitationFake is a signed-in fake whose application has the roles reviewer (role-1) and editor (role-2).
func invitationFake(t *testing.T) *fakeMultiPaaS {
	fake := projectFake(t)
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "reviewer", "--name", "Reviewer", "--json")
	fake.run(t, "roles", "create", "--app", "app-a-000", "--key", "editor", "--name", "Editor", "--json")
	fake.mutations, fake.scopes, fake.operations = nil, nil, nil
	return fake
}

func TestTenantsInviteSendsTheEmailAndEachRoleOnceInTheAppTenantsScope(t *testing.T) {
	fake := invitationFake(t)

	got := fake.run(t, "tenants", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--email", "ann@example.com",
		"--role", "reviewer", "--role", "role-2", "--role", "reviewer", "--json")

	if want := `{"invitation":{"id":"invitation-1","email":"ann@example.com","kind":"email","status":"pending","expiresAt":"2026-11-29T00:00:00.000Z","roles":[{"id":"role-1","key":"reviewer","name":"Reviewer"},{"id":"role-2","key":"editor","name":"Editor"}],"groups":[]}}`; compact(got.stdout) != want {
		t.Fatalf("exit %d: %s, want %s", got.exit, got.stdout, want)
	}
	wantSent := []map[string]any{{"input": map[string]any{"email": "ann@example.com", "groupIds": nil, "roleIds": []any{"role-1", "role-2"}, "scope": "app_tenant"}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
	if !reflect.DeepEqual(fake.emailed, []string{"ann@example.com"}) {
		t.Fatalf("emailed %v", fake.emailed)
	}
}

func TestTenantsInvitePrintsWhoWasInvitedWithWhichRoles(t *testing.T) {
	fake := invitationFake(t)

	got := fake.run(t, "tenants", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--email", "ann@example.com", "--role", "reviewer", "--role", "editor")

	if want := "Invited ann@example.com to app-tenant tenant-a with roles reviewer, editor (invitation invitation-1). They are emailed a link and hold the roles once they accept it, before 2026-11-29T00:00:00.000Z.\n"; got.exit != exitOK || got.stdout != want {
		t.Fatalf("exit %d: printed %q, want %q", got.exit, got.stdout, want)
	}
}

func TestTenantsInviteTakesTheAppAndTenantFromMultipaasJSON(t *testing.T) {
	fake := invitationFake(t)
	if err := os.WriteFile(projectFile, []byte(`{"applicationId": "app-a-000", "testAppTenantId": "tenant-a"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := fake.run(t, "tenants", "invite", "--email", "ann@example.com", "--role", "reviewer", "--json")

	if got.exit != exitOK || fake.scopes[len(fake.scopes)-1] != "client-a/app-a-000/tenant-a" {
		t.Fatalf("exit %d, scopes %v: %s", got.exit, fake.scopes, got.stdout)
	}
}

func TestTenantsInviteWithoutATenantIsRefusedBeforeAnyRequest(t *testing.T) {
	fake := invitationFake(t)
	if err := os.WriteFile(projectFile, []byte(`{"applicationId": "app-a-001", "testAppTenantId": "tenant-x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := fake.run(t, "tenants", "invite", "--app", "app-a-000", "--email", "ann@example.com", "--role", "reviewer", "--json")

	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if want := "--tenant is required: no multipaas.json here sets it for this application"; !strings.Contains(got.stdout, want) {
		t.Fatalf("stdout %s does not say %q", got.stdout, want)
	}
	if len(fake.operations) != 0 {
		t.Fatalf("sent %v", fake.operations)
	}
}

func TestTenantsInviteOfAnAppTenantYouCantReachIsRefusedBeforeSending(t *testing.T) {
	for name, tenant := range map[string]string{"unknown": "tenant-zz", "another application's": "tenant-b"} {
		t.Run(name, func(t *testing.T) {
			fake := invitationFake(t)
			sent := fake.watchScope("client-a/app-a-000/" + tenant)

			got := fake.run(t, "tenants", "invite", "--app", "app-a-000", "--tenant", tenant, "--email", "ann@example.com", "--role", "reviewer", "--json")

			assertFailure(t, got, exitRefused, "NOT_FOUND")
			if want := "no app-tenant " + tenant + " of application app-a-000 that you can reach"; !strings.Contains(got.stdout, want) {
				t.Fatalf("stdout %s does not say %q", got.stdout, want)
			}
			if len(fake.mutations) != 0 || len(fake.emailed) != 0 {
				t.Fatalf("sent %v, emailed %v", fake.mutations, fake.emailed)
			}
			if probes := slices.DeleteFunc(*sent, func(operation string) bool { return operation == "Me" }); len(probes) != 0 {
				t.Fatalf("sent %v in app-tenant %s's scope", probes, tenant)
			}
		})
	}
}

func TestTenantsMembersOfAnAppTenantInAnotherEnvironmentIsSentInItsScope(t *testing.T) {
	fake := invitationFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})
	fake.envTenants["env-two"] = []fakeRow{{id: "tenant-t", name: "Staging tenant"}}

	got := fake.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-t", "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-t" {
		t.Fatalf("scope %q", last)
	}
}

func TestTenantsMembersOfAnAppTenantTheServerEntersButDoesNotListIsSentInItsScope(t *testing.T) {
	fake := invitationFake(t)
	fake.unlisted = []string{"tenant-a"}

	got := fake.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-a", "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
}

func TestTenantsMembersOfAnAppTenantInARemovedEnvironmentIsRefusedBeforeSending(t *testing.T) {
	fake := invitationFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})
	fake.tenants["app-a-000"] = append(fake.tenants["app-a-000"], fakeRow{id: "tenant-t", name: "Staging tenant", env: "env-two"})
	if removed := fake.run(t, "envs", "remove", "--app", "app-a-000", "--env", "env-two", "--json"); removed.exit != exitOK {
		t.Fatalf("exit %d: %s %s", removed.exit, removed.stdout, removed.stderr)
	}
	sent := fake.watchScope("client-a/app-a-000/tenant-t")

	got := fake.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-t", "--json")

	assertFailure(t, got, exitRefused, "NOT_FOUND")
	if want := "no app-tenant tenant-t of application app-a-000 that you can reach"; !strings.Contains(got.stdout, want) {
		t.Fatalf("stdout %s does not say %q", got.stdout, want)
	}
	if probes := slices.DeleteFunc(*sent, func(operation string) bool { return operation == "Me" }); len(probes) != 0 {
		t.Fatalf("sent %v in the removed environment's app-tenant scope", probes)
	}
}

func TestTenantsInviteRefusalsTheCLIMakes(t *testing.T) {
	fake := invitationFake(t)
	invite := func(email string, roles ...string) result {
		args := []string{"tenants", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--email", email, "--json"}
		for _, r := range roles {
			args = append(args, "--role", r)
		}
		return fake.run(t, args...)
	}

	assertFailure(t, invite("ann@example.com"), exitRefused, "CLI_ERROR")
	assertFailure(t, invite("ann@example.com", "nope"), exitRefused, "NOT_FOUND")
	fake.roles = append(fake.roles, map[string]any{"id": "role-tenant", "key": "reviewer", "name": "Tenant reviewer", "appTenant": map[string]any{"id": "tenant-a"}, "permissions": []map[string]any{}})
	assertFailure(t, invite("ann@example.com", "reviewer"), exitRefused, "AMBIGUOUS_ROLE")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestTenantsInviteUnauthorizedNamesBothCauses(t *testing.T) {
	fake := invitationFake(t)
	fake.refuseInvitation = [2]string{"UNAUTHORIZED", "unauthorized"}
	want := "unauthorized: in app-tenant tenant-a you need access management, and every role given must carry only permissions you hold there"
	args := []string{"tenants", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--email", "ann@example.com", "--role", "reviewer", "--role", "editor"}

	asJSON := fake.run(t, append(args, "--json")...)
	plain := fake.run(t, args...)

	assertFailure(t, asJSON, exitRefused, "UNAUTHORIZED")
	if !strings.Contains(asJSON.stdout, `"message": "`+want+`"`) {
		t.Fatalf("stdout %s does not say %q", asJSON.stdout, want)
	}
	if plain.exit != exitRefused || plain.stderr != "multipaas: "+want+" (UNAUTHORIZED)\n" {
		t.Fatalf("exit %d, stderr %q", plain.exit, plain.stderr)
	}
}

func TestTenantsInviteOtherRefusalsPassThroughUnchanged(t *testing.T) {
	fake := invitationFake(t)
	fake.refuseInvitation = [2]string{"DUPLICATE_INVITATION", "an open invitation already exists for this address"}

	got := fake.run(t, "tenants", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--email", "ann@example.com", "--role", "reviewer")

	if want := "multipaas: an open invitation already exists for this address (DUPLICATE_INVITATION)\n"; got.exit != exitRefused || got.stderr != want {
		t.Fatalf("exit %d, stderr %q, want %q", got.exit, got.stderr, want)
	}
}

func TestTenantsInviteSignedOutExitsTwo(t *testing.T) {
	fake := signedOutFake(t)
	seedEntitlements(fake)

	got := fake.run(t, "tenants", "invite", "--app", "app-a-000", "--tenant", "tenant-a", "--email", "ann@example.com", "--role", "reviewer", "--json")

	assertFailure(t, got, exitSignedOut, "NOT_SIGNED_IN")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

// seedMembership gives tenant-a one member with no name, and an email and an import invitation that are pending, one
// that lapsed, and one pending in another app-tenant.
func seedMembership(fake *fakeMultiPaaS) {
	fake.members = map[string][]map[string]any{"tenant-a": {
		{"id": "member-1", "createdAt": "2026-11-20T00:00:00.000Z", "user": map[string]any{"id": "user-2", "email": "bob@example.com", "name": nil}},
	}}
	invitation := func(id, email, kind, status, tenant string) map[string]any {
		return map[string]any{
			"id": id, "email": email, "kind": kind, "status": status, "expiresAt": "2026-11-29T00:00:00.000Z",
			"appTenant": map[string]any{"id": tenant}, "roles": []map[string]any{{"id": "role-1", "key": "reviewer", "name": "Reviewer"}},
			"groups": []map[string]any{}, "dataImport": nil,
		}
	}
	fake.invitationRows = []map[string]any{
		invitation("invitation-1", "ann@example.com", "email", "pending", "tenant-a"),
		invitation("invitation-2", "cat@example.com", "import", "pending", "tenant-a"),
		invitation("invitation-3", "dan@example.com", "email", "expired", "tenant-a"),
		invitation("invitation-4", "eve@example.com", "email", "pending", "tenant-other"),
	}
}

func TestTenantsMembersListsMembersAndPendingInvitationsInTheAppTenantsScope(t *testing.T) {
	fake := invitationFake(t)
	seedMembership(fake)

	got := fake.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-a", "--json")

	want := `{"invitations":[` +
		`{"id":"invitation-1","email":"ann@example.com","kind":"email","status":"pending","expiresAt":"2026-11-29T00:00:00.000Z","roles":[{"id":"role-1","key":"reviewer","name":"Reviewer"}],"groups":[]},` +
		`{"id":"invitation-2","email":"cat@example.com","kind":"import","status":"pending","expiresAt":"2026-11-29T00:00:00.000Z","roles":[{"id":"role-1","key":"reviewer","name":"Reviewer"}],"groups":[]}],` +
		`"members":[{"id":"member-1","createdAt":"2026-11-20T00:00:00.000Z","user":{"id":"user-2","email":"bob@example.com","name":null}}]}`
	if compact(got.stdout) != want {
		t.Fatalf("exit %d: %s, want %s", got.exit, got.stdout, want)
	}
	for _, scope := range fake.scopes[len(fake.scopes)-2:] {
		if scope != "client-a/app-a-000/tenant-a" {
			t.Fatalf("scopes %v", fake.scopes)
		}
	}
	if want := []map[string]any{{"appTenantId": "tenant-a", "status": "pending"}}; !reflect.DeepEqual(fake.invitationWheres, want) {
		t.Fatalf("where %v, want %v", fake.invitationWheres, want)
	}
}

func TestTenantsMembersPrintsBothListsWithoutJSON(t *testing.T) {
	fake := invitationFake(t)
	seedMembership(fake)

	got := fake.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-a")

	want := "Members of app-tenant tenant-a (their roles are not listed):\n" +
		"EMAIL            NAME  USER ID  SINCE\n" +
		"bob@example.com        user-2   2026-11-20T00:00:00.000Z\n" +
		"Pending invitations you can manage:\n" +
		"EMAIL            ROLES     GROUPS  KIND    EXPIRES                   INVITATION ID\n" +
		"ann@example.com  reviewer          email   2026-11-29T00:00:00.000Z  invitation-1\n" +
		"cat@example.com  reviewer          import  2026-11-29T00:00:00.000Z  invitation-2\n"
	if got.exit != exitOK || got.stdout != want {
		t.Fatalf("exit %d: printed\n%s\nwant\n%s", got.exit, got.stdout, want)
	}
}

func TestTenantsMembersWithNoneSaysSo(t *testing.T) {
	fake := invitationFake(t)

	plain := fake.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-a")
	asJSON := fake.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-a", "--json")

	if want := "App-tenant tenant-a has no members.\nNo pending invitations you can manage.\n"; plain.stdout != want {
		t.Fatalf("printed %q, want %q", plain.stdout, want)
	}
	if want := `{"invitations":[],"members":[]}`; compact(asJSON.stdout) != want {
		t.Fatalf("printed %s, want %s", asJSON.stdout, want)
	}
}

func TestTenantsMembersReadsEveryPage(t *testing.T) {
	fake := invitationFake(t)
	fake.members = map[string][]map[string]any{}
	for i := range maxPageSize + 1 {
		fake.members["tenant-a"] = append(fake.members["tenant-a"], map[string]any{"id": fmt.Sprintf("member-%d", i), "createdAt": "2026-11-20T00:00:00.000Z", "user": map[string]any{"id": fmt.Sprintf("user-%d", i), "email": nil, "name": nil}})
		fake.invitationRows = append(fake.invitationRows, map[string]any{"id": fmt.Sprintf("invitation-%d", i), "email": "x@example.com", "kind": "email", "status": "pending", "appTenant": map[string]any{"id": "tenant-a"}, "roles": []map[string]any{}, "groups": []map[string]any{}})
	}

	got := fake.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-a", "--json")

	var listed struct{ Members, Invitations []map[string]any }
	decode(t, got.stdout, &listed)
	if len(listed.Members) != maxPageSize+1 || len(listed.Invitations) != maxPageSize+1 {
		t.Fatalf("listed %d members and %d invitations", len(listed.Members), len(listed.Invitations))
	}
}

func TestTenantsMembersTenantDefaultAndSignedOut(t *testing.T) {
	fake := invitationFake(t)
	if err := os.WriteFile(projectFile, []byte(`{"applicationId": "app-a-000", "testAppTenantId": "tenant-a"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fake.run(t, "tenants", "members", "--json"); got.exit != exitOK || fake.scopes[len(fake.scopes)-1] != "client-a/app-a-000/tenant-a" {
		t.Fatalf("exit %d, scopes %v: %s", got.exit, fake.scopes, got.stdout)
	}

	fake.operations = nil
	noTenant := fake.run(t, "tenants", "members", "--app", "app-a-001", "--json")
	assertFailure(t, noTenant, exitRefused, "CLI_ERROR")
	if len(fake.operations) != 0 {
		t.Fatalf("sent %v", fake.operations)
	}

	signedOut := signedOutFake(t)
	seedEntitlements(signedOut)
	assertFailure(t, signedOut.run(t, "tenants", "members", "--app", "app-a-000", "--tenant", "tenant-a", "--json"), exitSignedOut, "NOT_SIGNED_IN")
}

// runInvitationUpdate runs tenants invitation update on invitation id in tenant-a with each role, and --json.
func runInvitationUpdate(t *testing.T, fake *fakeMultiPaaS, id string, roles ...string) result {
	args := []string{"tenants", "invitation", "update", "--app", "app-a-000", "--tenant", "tenant-a", "--invitation", id, "--json"}
	for _, r := range roles {
		args = append(args, "--role", r)
	}
	return fake.run(t, args...)
}

func TestTenantsInvitationUpdateReplacesTheRolesAndKeepsTheGroups(t *testing.T) {
	fake := invitationFake(t)
	seedMembership(fake)
	fake.invitationRows[0]["groups"] = []map[string]any{{"id": "group-1", "name": "Admins"}}

	got := runInvitationUpdate(t, fake, "invitation-1", "editor", "role-1")

	if want := `{"invitation":{"id":"invitation-1","email":"ann@example.com","kind":"email","status":"pending","expiresAt":"2026-11-29T00:00:00.000Z","roles":[{"id":"role-2","key":"editor","name":"Editor"},{"id":"role-1","key":"reviewer","name":"Reviewer"}],"groups":[{"id":"group-1","name":"Admins"}]}}`; compact(got.stdout) != want {
		t.Fatalf("exit %d: %s, want %s", got.exit, got.stdout, want)
	}
	wantSent := []map[string]any{{"input": map[string]any{"id": "invitation-1", "roleIds": []any{"role-2", "role-1"}, "groupIds": []any{"group-1"}}}}
	if !reflect.DeepEqual(fake.mutations, wantSent) {
		t.Fatalf("sent %v, want %v", fake.mutations, wantSent)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000/tenant-a" {
		t.Fatalf("scope %q", last)
	}
	if want := []map[string]any{{"appTenantId": "tenant-a", "kind": "email", "status": "pending"}}; !reflect.DeepEqual(fake.invitationWheres, want) {
		t.Fatalf("where %v, want %v", fake.invitationWheres, want)
	}
}

func TestTenantsInvitationUpdatePrintsTheNewRolesAndAnyGroupsKept(t *testing.T) {
	fake := invitationFake(t)
	seedMembership(fake)
	args := []string{"tenants", "invitation", "update", "--app", "app-a-000", "--tenant", "tenant-a", "--invitation", "invitation-1", "--role", "editor"}

	withoutGroups := fake.run(t, args...)
	fake.invitationRows[0]["groups"] = []map[string]any{{"id": "group-1", "name": "Admins"}}
	withGroups := fake.run(t, args...)

	line := "Invitation invitation-1 for ann@example.com in app-tenant tenant-a now gives roles editor; they hold them once they accept.\n"
	if withoutGroups.stdout != line || withGroups.stdout != line+"It keeps its groups: Admins.\n" {
		t.Fatalf("printed %q and %q", withoutGroups.stdout, withGroups.stdout)
	}
	if sent := fake.mutations[0]["input"].(map[string]any)["groupIds"]; !reflect.DeepEqual(sent, []any{}) {
		t.Fatalf("sent groupIds %#v for an invitation with no groups, want []", sent)
	}
}

func TestTenantsInvitationUpdateRefusesAnInvitationItCannotChangeBeforeSending(t *testing.T) {
	for name, setUp := range map[string]func(*fakeMultiPaaS) string{
		"unknown or accepted or deleted": func(*fakeMultiPaaS) string { return "invitation-9" },
		"an import invitation":           func(*fakeMultiPaaS) string { return "invitation-2" },
		"an expired invitation":          func(*fakeMultiPaaS) string { return "invitation-3" },
		"another app-tenant's":           func(*fakeMultiPaaS) string { return "invitation-4" },
	} {
		t.Run(name, func(t *testing.T) {
			fake := invitationFake(t)
			seedMembership(fake)
			id := setUp(fake)

			got := runInvitationUpdate(t, fake, id, "editor")

			assertFailure(t, got, exitRefused, "NOT_FOUND")
			if want := "no pending email invitation " + id + " in app-tenant tenant-a that you can change"; !strings.Contains(got.stdout, want) {
				t.Fatalf("stdout %s does not say %q", got.stdout, want)
			}
			if len(fake.mutations) != 0 {
				t.Fatalf("sent %v", fake.mutations)
			}
		})
	}
}

func TestTenantsInvitationUpdateRefusalsOfTheNewRoles(t *testing.T) {
	fake := invitationFake(t)
	seedMembership(fake)

	assertFailure(t, runInvitationUpdate(t, fake, "invitation-1"), exitRefused, "CLI_ERROR")
	assertFailure(t, runInvitationUpdate(t, fake, "invitation-1", "nope"), exitRefused, "NOT_FOUND")
	fake.roles = append(fake.roles, map[string]any{"id": "role-tenant", "key": "editor", "name": "Tenant editor", "appTenant": map[string]any{"id": "tenant-a"}, "permissions": []map[string]any{}})
	assertFailure(t, runInvitationUpdate(t, fake, "invitation-1", "editor"), exitRefused, "AMBIGUOUS_ROLE")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v before the server refused anything", fake.mutations)
	}

	fake.refuseInvitation = [2]string{"UNAUTHORIZED", "unauthorized"}
	got := runInvitationUpdate(t, fake, "invitation-1", "role-tenant")
	assertFailure(t, got, exitRefused, "UNAUTHORIZED")
	if want := "unauthorized: in app-tenant tenant-a you need access management, and every role given must carry only permissions you hold there"; !strings.Contains(got.stdout, want) {
		t.Fatalf("stdout %s does not say %q", got.stdout, want)
	}
	if roles := fake.invitationRows[0]["roles"]; !reflect.DeepEqual(roles, []map[string]any{{"id": "role-1", "key": "reviewer", "name": "Reviewer"}}) {
		t.Fatalf("roles changed to %v", roles)
	}
}

func TestTenantsInvitationUpdateUnauthorizedReadCarriesTheHint(t *testing.T) {
	fake := invitationFake(t)
	seedMembership(fake)
	fake.refuseTenantRead = [2]string{"UNAUTHORIZED", "unauthorized"}

	got := runInvitationUpdate(t, fake, "invitation-1", "editor")

	assertFailure(t, got, exitRefused, "UNAUTHORIZED")
	if want := "unauthorized: in app-tenant tenant-a you need access management, and every role given must carry only permissions you hold there"; !strings.Contains(got.stdout, want) {
		t.Fatalf("stdout %s does not say %q", got.stdout, want)
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestTenantsInvitationUpdateOfOneAcceptedMeanwhileIsTheServersNotFound(t *testing.T) {
	fake := invitationFake(t)
	seedMembership(fake)
	fake.onGraphQL = func(operation string) {
		if operation == "UpdateInvitation" {
			fake.invitationRows = nil
		}
	}

	got := runInvitationUpdate(t, fake, "invitation-1", "editor")

	assertFailure(t, got, exitRefused, "NOT_FOUND")
	if !strings.Contains(got.stdout, `"message": "not found"`) {
		t.Fatalf("stdout %s", got.stdout)
	}
}

func TestTenantsInvitationUpdateTenantDefaultAndSignedOut(t *testing.T) {
	fake := invitationFake(t)
	seedMembership(fake)
	if err := os.WriteFile(projectFile, []byte(`{"applicationId": "app-a-000", "testAppTenantId": "tenant-a"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fake.run(t, "tenants", "invitation", "update", "--invitation", "invitation-1", "--role", "editor", "--json"); got.exit != exitOK || fake.scopes[len(fake.scopes)-1] != "client-a/app-a-000/tenant-a" {
		t.Fatalf("exit %d, scopes %v: %s", got.exit, fake.scopes, got.stdout)
	}

	fake.operations = nil
	noTenant := fake.run(t, "tenants", "invitation", "update", "--app", "app-a-001", "--invitation", "invitation-1", "--role", "editor", "--json")
	assertFailure(t, noTenant, exitRefused, "CLI_ERROR")
	if len(fake.operations) != 0 {
		t.Fatalf("sent %v", fake.operations)
	}

	signedOut := signedOutFake(t)
	seedEntitlements(signedOut)
	assertFailure(t, runInvitationUpdate(t, signedOut, "invitation-1", "editor"), exitSignedOut, "NOT_SIGNED_IN")
}
