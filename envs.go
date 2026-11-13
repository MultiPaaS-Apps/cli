package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// envsCommands list, create, rename, protect and remove an application's environments. Each environment keeps its own
// app-tenants, app-wide records and imports; the model is shared.
func envsCommands(command commandFunc) *cobra.Command {
	return group("envs", "An application's environments", envsList(command), envsCreate(command), envsRename(command),
		envsProtection(command, "protect", true), envsProtection(command, "unprotect", false), envsRemove(command),
		envsRedirectURIs(command))
}

func envsList(command commandFunc) *cobra.Command {
	var appID string
	cmd := command("list", "List an application's environments", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		list := []EnvironmentParts{}
		scope, err := s.appScope(ctx, appID, "")
		if err == nil {
			list, err = s.environmentsIn(ctx, scope)
		}
		return list, func(out io.Writer) {
			table(out, "NAME\tID\tDEFAULT\tPROTECTED", list, func(e EnvironmentParts) string {
				return e.Name + "\t" + e.Id + "\t" + strconv.FormatBool(e.IsDefault) + "\t" + strconv.FormatBool(e.DeletionProtected)
			})
		}, err
	})
	appFlag(cmd, &appID)
	return cmd
}

// environmentsIn lists the environments of the application scope names.
func (s *session) environmentsIn(ctx context.Context, scope string) ([]EnvironmentParts, error) {
	list := []EnvironmentParts{}
	err := s.call(ctx, scope, func(client graphql.Client) error {
		response, err := Environments(ctx, client)
		if err == nil {
			for _, item := range response.Me.Application.Environments {
				list = append(list, item.EnvironmentParts)
			}
		}
		return err
	})
	return list, err
}

// envsCreate needs a developer to hold access.manage.application on the application, and the server makes them the
// new environment's App manager. A deployer needs no permission and holds no role in the environment it creates.
func envsCreate(command commandFunc) *cobra.Command {
	var appID, name, signInClient string
	var redirectURIs []string
	cmd := command("create", "Create an environment to test your app in", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var created EnvironmentParts
		input := map[string]any{"name": name}
		if len(redirectURIs) > 0 {
			input["redirectUris"] = []map[string]any{{"oauthClientId": signInClient, "uris": redirectURIs}}
		}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := CreateEnvironment(ctx, client, input)
			if err == nil {
				created = response.CreateEnvironment.EnvironmentParts
			}
			return err
		})
		return map[string]EnvironmentParts{"environment": created}, func(out io.Writer) {
			fmt.Fprintf(out, "environment %s %q\n", created.Id, created.Name)
		}, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&name, "name", "", "the environment's name")
	_ = cmd.MarkFlagRequired("name")
	cmd.Flags().StringVar(&signInClient, "sign-in-client", "", "the sign-in client (oauth client id) the --redirect-uri values are for")
	cmd.Flags().StringArrayVar(&redirectURIs, "redirect-uri", nil, "a callback URL this environment accepts for --sign-in-client (repeatable)")
	cmd.MarkFlagsRequiredTogether("sign-in-client", "redirect-uri")
	return cmd
}

// envsRedirectURIs holds the callback URLs an environment accepts for a sign-in client, on top of the client's own.
func envsRedirectURIs(command commandFunc) *cobra.Command {
	return group("redirect-uris", "The sign-in callback URLs an environment accepts", envsRedirectURIsSet(command), envsRedirectURIsList(command))
}

type redirectURISet struct {
	OauthClientID string   `json:"oauthClientId"`
	Uris          []string `json:"uris"`
}

func envsRedirectURIsSet(command commandFunc) *cobra.Command {
	var appID, envID, signInClient string
	var redirectURIs []string
	cmd := command("set", "Replace the callback URLs a sign-in client accepts in this environment", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		set := redirectURISet{OauthClientID: signInClient, Uris: []string{}}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			uris := redirectURIs
			if uris == nil {
				uris = []string{}
			}
			response, err := SetEnvironmentRedirectUris(ctx, client, SetEnvironmentRedirectUrisInput{Id: envID, OauthClientId: signInClient, Uris: uris})
			if err != nil {
				return err
			}
			for _, item := range response.SetEnvironmentRedirectUris.RedirectUriSets {
				if item.OauthClientId == signInClient {
					set.Uris = item.Uris
				}
			}
			return nil
		})
		return set, func(out io.Writer) {
			if len(set.Uris) == 0 {
				fmt.Fprintln(out, "no redirect URIs")
			}
			for _, uri := range set.Uris {
				fmt.Fprintln(out, uri)
			}
		}, err
	})
	cmd.Long = "Replaces the callback URLs a sign-in client accepts in this environment, on top of the client's own list. " +
		"Pass no --redirect-uri to clear them. A deployer may do this only in environments it created."
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&envID, "env", "", "the environment's id (from envs list)")
	cmd.Flags().StringVar(&signInClient, "sign-in-client", "", "the sign-in client (oauth client id) the callback URLs are for")
	cmd.Flags().StringArrayVar(&redirectURIs, "redirect-uri", nil, "a callback URL this environment accepts for --sign-in-client (repeatable)")
	for _, flag := range []string{"env", "sign-in-client"} {
		_ = cmd.MarkFlagRequired(flag)
	}
	return cmd
}

func envsRedirectURIsList(command commandFunc) *cobra.Command {
	var appID, envID string
	cmd := command("list", "List the callback URLs each sign-in client accepts in this environment", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		sets := []redirectURISet{}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := EnvironmentRedirectUriSets(ctx, client)
			if err != nil {
				return err
			}
			for _, environment := range response.Me.Application.Environments {
				if environment.Id != envID {
					continue
				}
				for _, item := range environment.RedirectUriSets {
					sets = append(sets, redirectURISet{OauthClientID: item.OauthClientId, Uris: item.Uris})
				}
				return nil
			}
			return &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "environment not found"}
		})
		return sets, func(out io.Writer) {
			for _, set := range sets {
				for _, uri := range set.Uris {
					fmt.Fprintf(out, "%s\t%s\n", set.OauthClientID, uri)
				}
			}
		}, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&envID, "env", "", "the environment's id (from envs list)")
	_ = cmd.MarkFlagRequired("env")
	return cmd
}

// envsProtection turns an environment's deletion protection on or off.
func envsProtection(command commandFunc, verb string, protect bool) *cobra.Command {
	var appID, envID string
	short := "Turn an environment's deletion protection on"
	if !protect {
		short = "Turn an environment's deletion protection off"
	}
	cmd := command(verb, short, func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var changed EnvironmentParts
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := SetEnvironmentDeletionProtection(ctx, client, SetEnvironmentDeletionProtectionInput{Id: envID, Protected: protect})
			if err == nil {
				changed = response.SetEnvironmentDeletionProtection.EnvironmentParts
			}
			return err
		})
		return map[string]EnvironmentParts{"environment": changed}, func(out io.Writer) {
			state := "is not protected"
			if changed.DeletionProtected {
				state = "is protected"
			}
			fmt.Fprintf(out, "environment %s %q %s\n", changed.Id, changed.Name, state)
		}, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&envID, "env", "", "the environment's id (from envs list)")
	_ = cmd.MarkFlagRequired("env")
	return cmd
}

type removal struct {
	Removed bool   `json:"removed"`
	ID      string `json:"id"`
}

// envsRemove removes the environment at once; the server erases its data in the background.
func envsRemove(command commandFunc) *cobra.Command {
	var appID, envID string
	cmd := command("remove", "Remove an environment and erase its data for good", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var removed removal
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := RemoveEnvironment(ctx, client, RemoveEnvironmentInput{Id: envID})
			if err != nil {
				return err
			}
			if response.RemoveEnvironment.Id == "" {
				return &cliError{exit: exitRefused, Code: "CLI_ERROR", Message: "the server's answer did not say which environment was removed; run envs list to see whether it still exists"}
			}
			removed = removal{Removed: true, ID: response.RemoveEnvironment.Id}
			return nil
		})
		return removed, func(out io.Writer) {
			fmt.Fprintf(out, "environment %s is being removed; its data will be erased\n", removed.ID)
		}, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&envID, "env", "", "the environment's id (from envs list)")
	_ = cmd.MarkFlagRequired("env")
	return cmd
}

func envsRename(command commandFunc) *cobra.Command {
	var appID, envID, name string
	cmd := command("rename", "Rename an environment", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var renamed EnvironmentParts
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := RenameEnvironment(ctx, client, RenameEnvironmentInput{Id: envID, Name: name})
			if err == nil {
				renamed = response.RenameEnvironment.EnvironmentParts
			}
			return err
		})
		return map[string]EnvironmentParts{"environment": renamed}, func(out io.Writer) {
			fmt.Fprintf(out, "environment %s %q\n", renamed.Id, renamed.Name)
		}, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&envID, "env", "", "the environment's id (from envs list)")
	cmd.Flags().StringVar(&name, "name", "", "the environment's new name")
	for _, flag := range []string{"env", "name"} {
		_ = cmd.MarkFlagRequired(flag)
	}
	return cmd
}
