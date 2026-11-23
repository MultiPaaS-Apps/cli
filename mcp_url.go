package main

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/spf13/cobra"
)

// appsMCPURL prints the URL an application's end users add to Claude as a custom connector, to reach its
// records over MCP. Without an environment it needs no request and says it is the default environment's; with one it
// looks the environment up and names it. Stdout stays the bare URL, and the environment goes to stderr and, as
// "environment", into --json.
func appsMCPURL(command commandFunc) *cobra.Command {
	var appID, envID string
	cmd := command("mcp-url", "Print the MCP URL end users add to Claude to reach an application's records", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		url, environment, err := s.mcpURL(ctx, appID, envID)
		return map[string]any{"mcpUrl": url, "environment": environmentJSON(environment)}, func(out io.Writer) {
			fmt.Fprintln(out, url)
			fmt.Fprintln(s.env.stderr, environmentLabel(environment))
		}, err
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	cmd.Long = "Prints the MCP URL for an application's default environment, or for the environment --env names. " +
		"Without --env, inside a project directory for the same application, it is the environment saved in multipaas.json, if it has one. " +
		"The environment the URL is for is named on stderr and in --json's \"environment\"."
	return cmd
}

// environmentJSON is the environment a URL is for: the one looked up, or just {"isDefault": true} when none was named.
func environmentJSON(e *EnvironmentParts) any {
	if e == nil {
		return map[string]bool{"isDefault": true}
	}
	return e
}

func environmentLabel(e *EnvironmentParts) string {
	switch {
	case e == nil:
		return "for the default environment"
	case e.IsDefault:
		return fmt.Sprintf("for environment %q (the default environment)", e.Name)
	}
	return fmt.Sprintf("for environment %q (not the default environment)", e.Name)
}

// mcpURL is appID's MCP URL in envID. With no envID it is the default environment's plain URL and needs no request, so
// it works for any caller and returns no environment. An envID that is not among the environments the developer can
// see in appID is NOT_FOUND; the default environment's id gives the plain URL.
func (s *session) mcpURL(ctx context.Context, appID, envID string) (string, *EnvironmentParts, error) {
	base := s.origins.api + "/mcp/app/" + appID
	if envID == "" {
		return base, nil, nil
	}
	scope, err := s.appScope(ctx, appID, "")
	if err != nil {
		return "", nil, err
	}
	list, err := s.environmentsIn(ctx, scope)
	if err != nil {
		return "", nil, err
	}
	found := slices.IndexFunc(list, func(e EnvironmentParts) bool { return e.Id == envID })
	if found < 0 {
		return "", nil, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no environment " + envID + " of application " + appID + " that you can reach"}
	}
	if list[found].IsDefault {
		return base, &list[found], nil
	}
	return base + "/env/" + envID, &list[found], nil
}
