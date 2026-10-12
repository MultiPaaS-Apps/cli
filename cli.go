package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"text/tabwriter"
	"time"

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
	version        string
	apiURL         string // MULTIPAAS_API_URL, empty when unset
	origins        func(apiURL string) (origins, error)
	openBrowser    func(url string) error
}

// run parses args, runs one command, reports its result, and returns the exit code.
func run(args []string, env environment) int {
	var asJSON bool
	root := &cobra.Command{
		Use:           "multipaas",
		Short:         "Sign in to MultiPaaS and work with your apps",
		Version:       env.version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "print exactly one JSON document on stdout")

	var command commandFunc = func(use, short string, do func(context.Context, *session) (any, func(io.Writer), error)) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				hosts, err := env.origins(env.apiURL)
				if err != nil {
					return err
				}
				result, human, err := do(cmd.Context(), &session{origins: hosts, env: env, http: &http.Client{Timeout: requestTimeout, CheckRedirect: refuseRedirects}})
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
			if err := s.login(ctx); err != nil {
				return nil, nil, err
			}
			who, err := s.whoami(ctx)
			return who, func(out io.Writer) { fmt.Fprintf(out, "Signed in as %s\n", who.User.Email) }, err
		}),
		command("logout", "Sign out and revoke your tokens", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
			signedOut, err := s.logout(ctx)
			result := map[string]any{"signedOut": signedOut}
			return result, func(out io.Writer) { fmt.Fprintln(out, "Signed out") }, err
		}),
		command("whoami", "Show who you are signed in as", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
			who, err := s.whoami(ctx)
			return who, func(out io.Writer) { fmt.Fprintf(out, "Signed in as %s\n", who.User.Email) }, err
		}),
	)

	apps := &cobra.Command{Use: "apps", Short: "Applications you can reach"}
	apps.AddCommand(command("list", "List the applications you can reach", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		list, err := s.apps(ctx)
		return list, func(out io.Writer) {
			table(out, "CLIENT\tNAME\tID", list, func(a app) string { return a.ClientName + "\t" + a.Name + "\t" + a.ID })
		}, err
	}))

	var appID string
	tenants := &cobra.Command{Use: "tenants", Short: "App-tenants you can reach"}
	tenantsList := command("list", "List an application's app-tenants you can reach", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		list, err := s.tenants(ctx, appID)
		return list, func(out io.Writer) {
			table(out, "NAME\tID", list, func(t appTenant) string { return t.Name + "\t" + t.ID })
		}, err
	})
	appFlag(tenantsList, &appID)
	tenants.AddCommand(tenantsList)
	root.AddCommand(apps, tenants)
	root.AddCommand(modelCommands(command)...)
	root.AddCommand(projectCommands(command)...)
	root.AddCommand(recordCommands(command), oauthClientCommands(command))

	root.SetArgs(args)
	root.SetOut(env.stdout)
	root.SetErr(env.stderr)
	return report(env, asJSON, root.Execute())
}

// cliError is a failure carrying its exit code and the code and message --json reports.
type cliError struct {
	exit    int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *cliError) Error() string { return e.Message }

// report prints a failure (stdout as JSON, else stderr) and returns the exit code for it.
func report(env environment, asJSON bool, err error) int {
	if err == nil {
		return exitOK
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
