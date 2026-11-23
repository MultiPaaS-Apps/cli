package main

import (
	"os"
	"testing"
)

func TestAppsMCPURLPrintsTheDefaultEnvironmentsURL(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "apps", "mcp-url", "--app", "app-a-000", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	var printed struct {
		McpURL string `json:"mcpUrl"`
	}
	decode(t, got.stdout, &printed)
	if want := fake.server.URL + "/mcp/app/app-a-000"; printed.McpURL != want {
		t.Fatalf("mcpUrl %q, want %q", printed.McpURL, want)
	}
}

func TestAppsMCPURLPrintsJustTheURLWithoutJSON(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "apps", "mcp-url", "--app", "app-a-000")
	if got.exit != exitOK || got.stdout != fake.server.URL+"/mcp/app/app-a-000\n" {
		t.Fatalf("exit %d: %q", got.exit, got.stdout)
	}
}

func TestAppsMCPURLNamesANonDefaultEnvironment(t *testing.T) {
	fake := projectFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})
	got := fake.run(t, "apps", "mcp-url", "--app", "app-a-000", "--env", "env-two", "--json")
	var printed struct {
		McpURL string `json:"mcpUrl"`
	}
	decode(t, got.stdout, &printed)
	if want := fake.server.URL + "/mcp/app/app-a-000/env/env-two"; got.exit != exitOK || printed.McpURL != want {
		t.Fatalf("exit %d, mcpUrl %q, want %q", got.exit, printed.McpURL, want)
	}
}

func TestAppsMCPURLGivesThePlainURLForTheDefaultEnvironmentsID(t *testing.T) {
	fake := projectFake(t)
	got := fake.run(t, "apps", "mcp-url", "--app", "app-a-000", "--env", "env-default", "--json")
	var printed struct {
		McpURL string `json:"mcpUrl"`
	}
	decode(t, got.stdout, &printed)
	if want := fake.server.URL + "/mcp/app/app-a-000"; got.exit != exitOK || printed.McpURL != want {
		t.Fatalf("exit %d, mcpUrl %q, want %q", got.exit, printed.McpURL, want)
	}
}

func TestAppsMCPURLSaysWhichEnvironmentTheURLIsFor(t *testing.T) {
	fake := projectFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})

	bare := fake.run(t, "apps", "mcp-url", "--app", "app-a-000")
	if bare.exit != exitOK || bare.stdout != fake.server.URL+"/mcp/app/app-a-000\n" || bare.stderr != "for the default environment\n" {
		t.Fatalf("default: exit %d, stdout %q, stderr %q", bare.exit, bare.stdout, bare.stderr)
	}

	named := fake.run(t, "apps", "mcp-url", "--app", "app-a-000", "--env", "env-default")
	if named.exit != exitOK || named.stderr != "for environment \"Default\" (the default environment)\n" {
		t.Fatalf("named default: exit %d, stderr %q", named.exit, named.stderr)
	}

	if err := os.WriteFile(projectFile, []byte(`{"applicationId":"app-a-000","testAppTenantId":"tenant-t","environmentId":"env-two","apiVersion":"rev-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fromProject := fake.run(t, "apps", "mcp-url")
	if fromProject.exit != exitOK || fromProject.stderr != "for environment \"Staging\" (not the default environment)\n" {
		t.Fatalf("project: exit %d, stderr %q", fromProject.exit, fromProject.stderr)
	}

	asJSON := fake.run(t, "apps", "mcp-url", "--json")
	want := `{"environment":{"id":"env-two","name":"Staging","isDefault":false,"deletionProtected":false},"mcpUrl":"` + fake.server.URL + `/mcp/app/app-a-000/env/env-two"}`
	if asJSON.exit != exitOK || compact(asJSON.stdout) != want {
		t.Fatalf("json: exit %d, stdout %s, want %s", asJSON.exit, asJSON.stdout, want)
	}
}

func TestAppsMCPURLWithNoEnvironmentNeedsNoSessionAndNoRequest(t *testing.T) {
	for name, fake := range map[string]*fakeMultiPaaS{"no session": signedOutFake(t), "deployer": deployerFake(t)} {
		t.Run(name, func(t *testing.T) {
			got := fake.run(t, "apps", "mcp-url", "--app", "app-a-000", "--json")
			want := `{"environment":{"isDefault":true},"mcpUrl":"` + fake.server.URL + `/mcp/app/app-a-000"}`
			if got.exit != exitOK || compact(got.stdout) != want || len(fake.scopes) != 0 || fake.deployerMints != 0 {
				t.Fatalf("exit %d, stdout %s, want %s, scopes %v, mints %d", got.exit, got.stdout, want, fake.scopes, fake.deployerMints)
			}
		})
	}
}

func TestAppsMCPURLRefusesAnEnvironmentTheAppDoesNotHave(t *testing.T) {
	fake := projectFake(t)
	assertFailure(t, fake.run(t, "apps", "mcp-url", "--app", "app-a-000", "--env", "env-nope", "--json"), exitRefused, "NOT_FOUND")
}

func TestAppsMCPURLTakesTheAppAndEnvironmentFromTheProject(t *testing.T) {
	fake := projectFake(t)
	fake.environments["app-a-000"] = append(fake.environments["app-a-000"], fakeEnv{id: "env-two", name: "Staging"})
	if err := os.WriteFile(projectFile, []byte(`{"applicationId":"app-a-000","testAppTenantId":"tenant-t","environmentId":"env-two","apiVersion":"rev-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := fake.run(t, "apps", "mcp-url")
	if got.exit != exitOK || got.stdout != fake.server.URL+"/mcp/app/app-a-000/env/env-two\n" {
		t.Fatalf("exit %d: %q", got.exit, got.stdout)
	}
}
