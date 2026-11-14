package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// The exit codes the CLI reports; the plugin's skill documents the same table.
const (
	exitOK        = 0
	exitRefused   = 1
	exitSignedOut = 2
	exitUpgrade   = 3
)

// requestTimeout bounds every request, so a server that never answers cannot hold the credentials lock.
var requestTimeout = 30 * time.Second

// refuseRedirects stops a redirect from re-sending a request, and the tokens in it, to another URL.
func refuseRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// environment is what the CLI touches outside itself. main passes the real one; tests pass a fake server's origins.
type environment struct {
	stdout, stderr io.Writer
	stdin          io.Reader
	version        string
	apiURL         string // MULTIPAAS_API_URL, empty when unset
	profile        string // MULTIPAAS_PROFILE, empty when unset
	clientID       string // MULTIPAAS_CLIENT_ID: a deployer CI signs in as, empty when unset
	clientSecret   string // MULTIPAAS_CLIENT_SECRET
	origins        func(apiURL string) (origins, error)
	openBrowser    func(url string) error
}

// run parses args, runs one command, reports its result, and returns the exit code.
func run(args []string, env environment) int {
	var asJSON bool
	var profileFlag string
	var ran *session // the session the command ran as, for withProfileHint
	root := &cobra.Command{
		Use:           "multipaas",
		Short:         "Sign in to MultiPaaS and work with your apps",
		Version:       env.version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "print exactly one JSON document on stdout")
	root.PersistentFlags().StringVar(&profileFlag, "profile", "", "the developer sign-in to use (default $MULTIPAAS_PROFILE, else your unnamed sign-in)")

	var command commandFunc = func(use, short string, do func(context.Context, *session) (any, func(io.Writer), error)) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				profile, err := chooseProfile(profileFlag, env.profile)
				if err != nil {
					return err
				}
				hosts, err := env.origins(env.apiURL)
				if err != nil {
					return err
				}
				ran = &session{origins: hosts, profile: profile, env: env, http: &http.Client{Timeout: requestTimeout, CheckRedirect: refuseRedirects}}
				result, human, err := do(cmd.Context(), ran)
				if err != nil {
					return err
				}
				if asJSON {
					return writeJSON(env.stdout, result)
				}
				human(env.stdout)
				return nil
			},
		}
	}

	root.AddCommand(
		command("login", "Sign in through your browser", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
			if err := s.refuseAsDeployer(); err != nil {
				return nil, nil, err
			}
			if err := s.login(ctx); err != nil {
				return nil, nil, err
			}
			who, err := s.whoami(ctx)
			return who, func(out io.Writer) { fmt.Fprintf(out, "Signed in as %s%s\n", who.User.Email, s.profileSuffix()) }, err
		}),
		command("logout", "Sign out and revoke your tokens", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
			if err := s.refuseAsDeployer(); err != nil {
				return nil, nil, err
			}
			signedOut, err := s.logout(ctx)
			result := map[string]any{"signedOut": signedOut, "profile": s.profileJSON()}
			return result, func(out io.Writer) { fmt.Fprintln(out, "Signed out"+s.profileSuffix()) }, err
		}),
		command("whoami", "Show who you are signed in as", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
			who, err := s.whoami(ctx)
			return who, func(out io.Writer) { fmt.Fprintf(out, "Signed in as %s%s\n", who.User.Email, s.profileSuffix()) }, err
		}),
	)

	apps := &cobra.Command{Use: "apps", Short: "Applications you can reach"}
	apps.AddCommand(command("list", "List the applications you can reach", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		list, err := s.apps(ctx)
		return list, func(out io.Writer) {
			table(out, "CLIENT\tNAME\tID", list, func(a app) string { return a.ClientName + "\t" + a.Name + "\t" + a.ID })
		}, err
	}), appsCreate(command))

	var appID, envID string
	var includeRetired bool
	tenants := &cobra.Command{Use: "tenants", Short: "App-tenants you can reach"}
	tenantsList := command("list", "List an application's app-tenants you can reach", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		list, err := s.testTenants(ctx, appID, envID, includeRetired)
		return list, func(out io.Writer) {
			if includeRetired {
				table(out, "NAME\tID\tRETIRED", list, func(t appTenant) string { return t.Name + "\t" + t.ID + "\t" + t.RetiredAt })
				return
			}
			table(out, "NAME\tID", list, func(t appTenant) string { return t.Name + "\t" + t.ID })
		}, err
	})
	appFlag(tenantsList, &appID)
	tenantsEnvFlag(tenantsList, &envID)
	tenantsList.Flags().BoolVar(&includeRetired, "include-retired", false, "list retired app-tenants too, each with when it was retired")
	tenants.AddCommand(tenantsList, tenantsCreate(command), tenantsRetire(command))
	root.AddCommand(apps, tenants, envsCommands(command))
	root.AddCommand(modelCommands(command)...)
	root.AddCommand(projectCommands(command)...)
	root.AddCommand(recordCommands(command), roleCommands(command), oauthClientCommands(command), appIdentityCommands(command), deployerCommands(command), importCommands(command), feedbackCommands(command), apiCommand(command))

	root.SetArgs(args)
	root.SetOut(env.stdout)
	root.SetErr(env.stderr)
	return report(env, asJSON, withProfileHint(ran, root.Execute()))
}

// appsCreate makes an application in one of the developer's clients. It needs client.applications.create, and the
// server makes its creator the new application's App manager.
func appsCreate(command commandFunc) *cobra.Command {
	var clientID, name string
	cmd := command("create", "Create an application in one of your clients", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var created app
		owner, err := s.creatableClient(ctx, clientID)
		if err == nil {
			err = s.call(ctx, owner.Id, func(client graphql.Client) error {
				response, err := CreateApplication(ctx, client, CreateApplicationInput{Name: name})
				if err == nil {
					created = app{ClientID: owner.Id, ClientName: owner.Name, ID: response.CreateApplication.Id, Name: response.CreateApplication.Name}
				}
				return err
			})
		}
		return map[string]app{"application": created}, func(out io.Writer) {
			fmt.Fprintf(out, "application %s %q in %s\n", created.ID, created.Name, created.ClientName)
		}, err
	})
	cmd.Flags().StringVar(&clientID, "client", "", "the client to create it in (needed when you can reach more than one)")
	cmd.Flags().StringVar(&name, "name", "", "the application's name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// tenantsCreate makes an app-tenant to build and test an app in. It needs access.manage.application on the
// application, the same permission the web app's app-tenants page asks for.
func tenantsCreate(command commandFunc) *cobra.Command {
	var appID, envID, name string
	cmd := command("create", "Create an app-tenant to test your app in", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var created appTenant
		scope, err := s.scopeIn(ctx, appID, envID)
		if err == nil {
			err = s.call(ctx, scope, func(client graphql.Client) error {
				response, err := CreateAppTenant(ctx, client, CreateAppTenantInput{Name: name})
				if err == nil {
					created = appTenant{ID: response.CreateAppTenant.Id, Name: response.CreateAppTenant.Name}
				}
				return err
			})
		}
		return map[string]appTenant{"appTenant": created}, func(out io.Writer) {
			fmt.Fprintf(out, "app-tenant %s %q\n", created.ID, created.Name)
		}, err
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	cmd.Flags().StringVar(&name, "name", "", "the app-tenant's name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// tenantsRetire retires an app-tenant: its members and every token for it stop working at once, and its records
// stay. It needs access.manage.application, the same permission as tenants create, and finds the app-tenant only in
// the environment it runs in.
func tenantsRetire(command commandFunc) *cobra.Command {
	var appID, envID, tenantID string
	cmd := command("retire <app-tenant-id>", "Retire an app-tenant; its members and tokens stop working", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var retired retirement
		err := s.inEnv(ctx, appID, envID, func(client graphql.Client) error {
			response, err := DeactivateAppTenant(ctx, client, DeactivateAppTenantInput{Id: tenantID})
			if err == nil {
				retired = retirement{Retired: true, ID: response.DeactivateAppTenant.Id}
			}
			return err
		})
		return retired, func(out io.Writer) {
			fmt.Fprintf(out, "app-tenant %s is retired; its members and tokens stop working now\n", retired.ID)
		}, err
	})
	cmd.Args = oneID(&tenantID)
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	return cmd
}

// retirement is what tenants retire prints.
type retirement struct {
	Retired bool   `json:"retired"`
	ID      string `json:"id"`
}

// cliError is a failure carrying its exit code and the code, message and field errors --json reports.
type cliError struct {
	exit        int
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	FieldErrors []fieldError `json:"fieldErrors,omitempty"`
	Reason      string       `json:"reason,omitempty"`
}

// fieldError is one field a VALIDATION refusal names, as the server sends it.
type fieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

func (e *cliError) Error() string { return e.Message }

// report prints a failure (stdout as JSON, else stderr) and returns the exit code for it. A response the api
// command already printed only sets the exit code.
func report(env environment, asJSON bool, err error) int {
	if err == nil {
		return exitOK
	}
	var printed *printedResponse
	if errors.As(err, &printed) {
		return printed.exit
	}
	failure := asCLIError(err)
	if asJSON {
		_ = writeJSON(env.stdout, map[string]*cliError{"error": failure})
	} else {
		fmt.Fprintf(env.stderr, "multipaas: %s (%s)\n", failure.Message, failure.Code)
	}
	return failure.exit
}

// asCLIError gives every failure an exit code: a refusal keeps the server's code, anything else is CLI_ERROR.
func asCLIError(err error) *cliError {
	var failure *cliError
	if errors.As(err, &failure) {
		return failure
	}
	var refusal *oauthError
	if errors.As(err, &refusal) {
		return &cliError{exit: exitRefused, Code: refusal.Code, Message: refusal.Description}
	}
	return &cliError{exit: exitRefused, Code: "CLI_ERROR", Message: err.Error()}
}

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func table[T any](out io.Writer, header string, rows []T, line func(T) string) {
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, header)
	for _, row := range rows {
		fmt.Fprintln(writer, line(row))
	}
	_ = writer.Flush()
}

// textOrFile is text, or the contents of file when one is given, where - is stdin. flag names the file flag in a
// read failure.
func textOrFile(stdin io.Reader, text, file, flag string) (string, error) {
	if file == "" {
		return text, nil
	}
	var data []byte
	var err error
	if file == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(file)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", flag, err)
	}
	return string(data), nil
}
