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
