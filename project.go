package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// projectFile is an app's own MultiPaaS config, in the directory the CLI runs in.
const projectFile = "multipaas.json"

// projectConfig is multipaas.json: the application an app is built on, the app-tenant it is tested
// in, the API version it is pinned to, and the OAuth client its end users sign in through.
type projectConfig struct {
	ApplicationID   string `json:"applicationId"`
	TestAppTenantID string `json:"testAppTenantId"`
	APIVersion      string `json:"apiVersion"`
	OAuthClientID   string `json:"oauthClientId,omitempty"`
}

// endpoints are where an app built on the application sends its end users and its requests.
type endpoints struct {
	GraphQL   string `json:"graphql"`
	Authorize string `json:"authorize"`
	Token     string `json:"token"`
	Revoke    string `json:"revoke"`
}

// revision is one of an application's API versions.
type revision struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	Pinned    bool   `json:"pinned"`
}

var errNotInitialized = &cliError{exit: exitRefused, Code: "NOT_INITIALIZED", Message: "no multipaas.json with an applicationId here: run multipaas init first"}

// projectCommands are init, pin, versions list|diff and schema pull: what pins an app to an API version.
func projectCommands(command commandFunc) []*cobra.Command {
	return []*cobra.Command{
		initProject(command),
		pin(command),
		group("versions", "An application's API versions", versionsList(command), versionsDiff(command)),
		group("schema", "An API version's GraphQL schema", schemaPull(command)),
	}
}

func initProject(command commandFunc) *cobra.Command {
	var appID, tenantID, wanted string
	cmd := command("init", "Write multipaas.json: the application, test app-tenant and API version an app is built on", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		tenants, err := s.tenants(ctx, appID)
		if err != nil {
			return nil, nil, err
		}
		if !slices.ContainsFunc(tenants, func(t appTenant) bool { return t.ID == tenantID }) {
			return nil, nil, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no app-tenant " + tenantID + " of application " + appID + " that you can reach"}
		}
		pinned, err := s.pinnable(ctx, appID, wanted)
		if err != nil {
			return nil, nil, err
		}
		config, err := updateProject(func(config *projectConfig) {
			if config.ApplicationID != appID {
				config.OAuthClientID = ""
			}
			config.ApplicationID, config.TestAppTenantID, config.APIVersion = appID, tenantID, pinned
		})
		result := map[string]any{"config": config, "endpoints": s.endpoints(appID)}
		return result, func(out io.Writer) {
			fmt.Fprintf(out, "Wrote %s: application %s, test app-tenant %s, API version %q\n", projectFile, appID, tenantID, pinned)
		}, err
	})
	appFlag(cmd, &appID)
	tenantFlag(cmd, &appID, &tenantID, nil)
	cmd.Flags().StringVar(&wanted, "api-version", "", "the API version to pin (default: the newest)")
	return cmd
}

func pin(command commandFunc) *cobra.Command {
	var wanted string
	cmd := command("pin", "Pin multipaas.json to one of the application's API versions", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		config, err := readProject()
		if err != nil {
			return nil, nil, err
		}
		if config.ApplicationID == "" {
			return nil, nil, errNotInitialized
		}
		pinned, err := s.pinnable(ctx, config.ApplicationID, wanted)
		if err != nil {
			return nil, nil, err
		}
		if pinned == "" {
			return nil, nil, &cliError{exit: exitRefused, Code: "NO_API_VERSION", Message: "application " + config.ApplicationID + " has no API version yet: change its model first"}
		}
		config, err = updateProject(func(config *projectConfig) { config.APIVersion = pinned })
		return config, func(out io.Writer) { fmt.Fprintf(out, "Pinned API version %s\n", pinned) }, err
	})
	cmd.Flags().StringVar(&wanted, "api-version", "", "the API version to pin (default: the newest)")
	return cmd
}

func versionsList(command commandFunc) *cobra.Command {
	var appID string
	cmd := command("list", "List an application's API versions, newest first", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		list, err := s.revisions(ctx, appID)
		if err != nil {
			return nil, nil, err
		}
		config, err := readProject()
		for i := range list {
			list[i].Pinned = config.ApplicationID == appID && list[i].ID == config.APIVersion
		}
		return list, func(out io.Writer) {
			table(out, "ID\tCREATED\tPINNED", list, func(r revision) string {
				return r.ID + "\t" + r.CreatedAt + "\t" + map[bool]string{true: "pinned"}[r.Pinned]
			})
		}, err
	})
	appFlag(cmd, &appID)
	return cmd
}

// versionsDiffResult is what versions diff prints: the two API versions and what changed between them.
type versionsDiffResult struct {
	From string `json:"from"`
	To   string `json:"to"`
	versionDiff
}

func versionsDiff(command commandFunc) *cobra.Command {
	var appID, from, to string
	cmd := command("diff", "Show what changed between two API versions: breaking, dangerous and added", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		if from == "" {
			config, err := readProject()
			if err != nil {
				return nil, nil, err
			}
			if config.ApplicationID != appID || config.APIVersion == "" {
				return nil, nil, &cliError{exit: exitRefused, Code: "NOT_PINNED", Message: "multipaas.json pins no API version of application " + appID + ": run multipaas pin, or pass --from"}
			}
			from = config.APIVersion
		}
		if to == "" {
			newest, err := s.pinnable(ctx, appID, "")
			if err != nil {
				return nil, nil, err
			}
			if newest == "" {
				return nil, nil, &cliError{exit: exitRefused, Code: "NO_API_VERSION", Message: "application " + appID + " has no API version yet: change its model first"}
			}
			to = newest
		}
		var diffed *ApiSchemaDiffResponse
		err := s.inApp(ctx, appID, func(client graphql.Client) (err error) {
			diffed, err = ApiSchemaDiff(ctx, client, from, to)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		result := versionsDiffResult{From: from, To: to, versionDiff: diffed.ApiSchemaDiff}
		return result, func(out io.Writer) { printVersionsDiff(out, result) }, nil
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&from, "from", "", "the API version to compare from (default: multipaas.json's apiVersion)")
	cmd.Flags().StringVar(&to, "to", "", "the API version to compare to (default: the newest)")
	return cmd
}

// printVersionsDiff prints each kind of change under its own heading, or that there are none.
func printVersionsDiff(out io.Writer, diff versionsDiffResult) {
	fmt.Fprintf(out, "API version %s to %s\n", diff.From, diff.To)
	sections := []struct {
		heading string
		changes []schemaChange
	}{{"Breaking", diff.Breaking}, {"Dangerous", diff.Dangerous}, {"Added", diff.Added}}
	none := true
	for _, section := range sections {
		if len(section.changes) == 0 {
			continue
		}
		none = false
		fmt.Fprintf(out, "\n%s:\n", section.heading)
		for _, change := range section.changes {
			fmt.Fprintf(out, "  %s\n", change.Description)
		}
	}
	if none {
		fmt.Fprintln(out, "No changes.")
	}
}

func schemaPull(command commandFunc) *cobra.Command {
	var appID, wanted, path string
	cmd := command("pull", "Write an API version's GraphQL schema, for your app's codegen", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		if wanted == "" {
			config, err := readProject()
			if err != nil {
				return nil, nil, err
			}
			if config.ApplicationID != appID || config.APIVersion == "" {
				return nil, nil, &cliError{exit: exitRefused, Code: "NOT_PINNED", Message: "multipaas.json pins no API version of application " + appID + ": run multipaas pin, or pass --api-version"}
			}
			wanted = config.APIVersion
		}
		var pulled *ApiSchemaResponse
		err := s.inApp(ctx, appID, func(client graphql.Client) (err error) {
			pulled, err = ApiSchema(ctx, client, wanted)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(path, []byte(pulled.ApiSchema), 0o644); err != nil {
			return nil, nil, err
		}
		result := map[string]string{"apiVersion": wanted, "path": path}
		return result, func(out io.Writer) { fmt.Fprintf(out, "Wrote API version %s's schema to %s\n", wanted, path) }, nil
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&wanted, "api-version", "", "the API version (default: multipaas.json's apiVersion)")
	cmd.Flags().StringVar(&path, "out", "multipaas.graphql", "the file to write")
	return cmd
}

// pinnable is wanted when it is one of appID's API versions, or the newest when wanted is "" ("" when
// the application has none yet).
func (s *session) pinnable(ctx context.Context, appID, wanted string) (string, error) {
	list, err := s.revisions(ctx, appID)
	if err != nil {
		return "", err
	}
	if wanted == "" {
		if len(list) == 0 {
			return "", nil
		}
		return list[0].ID, nil
	}
	if !slices.ContainsFunc(list, func(r revision) bool { return r.ID == wanted }) {
		return "", &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no API version " + wanted + " of application " + appID}
	}
	return wanted, nil
}

// revisions are appID's API versions, newest first.
func (s *session) revisions(ctx context.Context, appID string) ([]revision, error) {
	scope, err := s.appScope(ctx, appID, "")
	if err != nil {
		return nil, err
	}
	return pages(func(page PageInput) ([]revision, bool, error) {
		var listed *ModelRevisionsResponse
		err := s.call(ctx, scope, func(client graphql.Client) (err error) {
			listed, err = ModelRevisions(ctx, client, page)
			return err
		})
		if err != nil {
			return nil, false, err
		}
		found := []revision{}
		for _, item := range listed.ModelRevisions.Items {
			found = append(found, revision{ID: item.Id, CreatedAt: item.CreatedAt})
		}
		return found, listed.ModelRevisions.PageInfo.HasMore, nil
	})
}

func (s *session) endpoints(appID string) endpoints {
	return endpoints{
		GraphQL:   s.origins.api + "/app/" + appID + "/graphql",
		Authorize: s.origins.api + "/oauth/authorize",
		Token:     s.origins.api + "/oauth/token",
		Revoke:    s.origins.api + "/oauth/revoke",
	}
}

// tenantFlag is --tenant, which defaults to multipaas.json's testAppTenantId when that file is for appID.
// A non-nil skip that is true leaves --tenant unset without a default or a refusal.
func tenantFlag(cmd *cobra.Command, appID, tenantID *string, skip *bool) {
	cmd.Flags().StringVar(tenantID, "tenant", "", "the app-tenant's id (from tenants list; default: multipaas.json's testAppTenantId for the same application)")
	defaultFrom(cmd, tenantID, "tenant", skip, func(config projectConfig) string {
		if config.ApplicationID != *appID {
			return ""
		}
		return config.TestAppTenantID
	})
}

// defaultFrom fills an unset flag from multipaas.json before the command runs, and refuses when
// neither gives it a value. A non-nil skip that is true does neither.
func defaultFrom(cmd *cobra.Command, value *string, flag string, skip *bool, pick func(projectConfig) string) {
	previous := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if previous != nil {
			if err := previous(cmd, args); err != nil {
				return err
			}
		}
		if *value != "" || (skip != nil && *skip) {
			return nil
		}
		config, err := readProject()
		if err != nil {
			return err
		}
		if *value = pick(config); *value == "" {
			return fmt.Errorf("--%s is required: no %s here sets it for this application", flag, projectFile)
		}
		return nil
	}
}

// readProject is multipaas.json, or an empty config when there is none.
func readProject() (projectConfig, error) {
	config := projectConfig{}
	data, err := os.ReadFile(projectFile)
	if errors.Is(err, fs.ErrNotExist) {
		return config, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &config)
	}
	if err != nil {
		return config, fmt.Errorf("read %s: %w", projectFile, err)
	}
	return config, nil
}

// updateProject changes multipaas.json, creating it when there is none.
func updateProject(change func(*projectConfig)) (projectConfig, error) {
	config, err := readProject()
	if err != nil {
		return config, err
	}
	change(&config)
	data, err := json.MarshalIndent(config, "", "  ")
	if err == nil {
		err = os.WriteFile(projectFile, append(data, '\n'), 0o644)
	}
	return config, err
}
