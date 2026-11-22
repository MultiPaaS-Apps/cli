package main

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/spf13/cobra"
)

// appsMCPURL prints the URL an application's end users add to Claude or ChatGPT as a custom connector, to reach its
// records over MCP. Without an environment it needs no request; with one it checks the environment is the
// application's, and the default environment's id gives the plain URL.
func appsMCPURL(command commandFunc) *cobra.Command {
	var appID, envID string
	cmd := command("mcp-url", "Print the MCP URL end users add to Claude or ChatGPT to reach an application's records", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		url, err := s.mcpURL(ctx, appID, envID)
		return map[string]string{"mcpUrl": url}, func(out io.Writer) { fmt.Fprintln(out, url) }, err
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	return cmd
}

// mcpURL is appID's MCP URL in envID: the default environment's when envID is "" or names the default. An envID that
// is not among the environments the developer can see in appID is NOT_FOUND.
func (s *session) mcpURL(ctx context.Context, appID, envID string) (string, error) {
	base := s.origins.api + "/mcp/app/" + appID
	if envID == "" {
		return base, nil
	}
	scope, err := s.appScope(ctx, appID, "")
	if err != nil {
		return "", err
	}
	list, err := s.environmentsIn(ctx, scope)
	if err != nil {
		return "", err
	}
	found := slices.IndexFunc(list, func(e EnvironmentParts) bool { return e.Id == envID })
	if found < 0 {
		return "", &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no environment " + envID + " of application " + appID + " that you can reach"}
	}
	if list[found].IsDefault {
		return base, nil
	}
	return base + "/env/" + envID, nil
}
