package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// oauthClientCommands register and change the OAuth clients an app's end users sign in through. Each
// needs application.manage_oauth on the application.
func oauthClientCommands(command commandFunc) *cobra.Command {
	return group("oauth-client", "Register and change your app's end-user sign-in client",
		oauthClientList(command), oauthClientCreate(command), oauthClientUpdate(command))
}

func oauthClientList(command commandFunc) *cobra.Command {
	var appID string
	cmd := command("list", "List the application's sign-in clients", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		list := []OAuthClientParts{}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			listed, err := OAuthClients(ctx, client)
			if err != nil {
				return err
			}
			list = list[:0]
			for _, item := range listed.OauthClients {
				list = append(list, item.OAuthClientParts)
			}
			return nil
		})
		return list, func(out io.Writer) {
			table(out, "NAME\tID\tREDIRECT URIS", list, func(c OAuthClientParts) string {
				return c.Name + "\t" + c.Id + "\t" + strings.Join(c.RedirectUris, " ")
			})
		}, err
	})
	appFlag(cmd, &appID)
	return cmd
}

func oauthClientCreate(command commandFunc) *cobra.Command {
	var appID, name string
	var redirects []string
	cmd := command("create", "Register a sign-in client; multipaas.json remembers it", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var created OAuthClientParts
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := CreateOAuthClient(ctx, client, CreateOAuthClientInput{Name: name, RedirectUris: redirects})
			if err == nil {
				created = response.CreateOAuthClient.OAuthClientParts
			}
			return err
		})
		if err == nil {
			if err = rememberClient(appID, created.Id); err != nil {
				err = fmt.Errorf("registered oauth client %s, but could not save it to %s: %w", created.Id, projectFile, err)
			}
		}
		return map[string]OAuthClientParts{"oauthClient": created}, printClient(created), err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&name, "name", "", "the client's name")
	cmd.Flags().StringArrayVar(&redirects, "redirect-uri", nil, "a URL your app receives the sign-in at (https, or http on localhost); repeat for more")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("redirect-uri")
	return cmd
}

func oauthClientUpdate(command commandFunc) *cobra.Command {
	var appID, id, name string
	var redirects []string
	var cmd *cobra.Command
	cmd = command("update", "Rename a sign-in client or replace its redirect URIs; what is not given is kept", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var updated OAuthClientParts
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			listed, err := OAuthClients(ctx, client)
			if err != nil {
				return err
			}
			i := slices.IndexFunc(listed.OauthClients, func(c OAuthClientsOauthClientsOAuthClient) bool { return c.Id == id })
			if i < 0 {
				return &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no sign-in client " + id + " in application " + appID}
			}
			input := UpdateOAuthClientInput{Id: id, Name: listed.OauthClients[i].Name, RedirectUris: listed.OauthClients[i].RedirectUris}
			if cmd.Flags().Changed("name") {
				input.Name = name
			}
			if cmd.Flags().Changed("redirect-uri") {
				input.RedirectUris = redirects
			}
			response, err := UpdateOAuthClient(ctx, client, input)
			if err == nil {
				updated = response.UpdateOAuthClient.OAuthClientParts
			}
			return err
		})
		return map[string]OAuthClientParts{"oauthClient": updated}, printClient(updated), err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&id, "id", "", "the client's id (from oauth-client list; default: multipaas.json's oauthClientId for the same application)")
	defaultFrom(cmd, &id, "id", nil, func(config projectConfig) string {
		if config.ApplicationID != appID {
			return ""
		}
		return config.OAuthClientID
	})
	cmd.Flags().StringVar(&name, "name", "", "the client's new name")
	cmd.Flags().StringArrayVar(&redirects, "redirect-uri", nil, "the client's redirect URIs, replacing them all; repeat for more")
	return cmd
}

// rememberClient writes a new client's id into multipaas.json, when that file is for appID.
func rememberClient(appID, clientID string) error {
	config, err := readProject()
	if err != nil || config.ApplicationID != appID {
		return err
	}
	_, err = updateProject(func(config *projectConfig) { config.OAuthClientID = clientID })
	return err
}

func printClient(c OAuthClientParts) func(io.Writer) {
	return func(out io.Writer) {
		fmt.Fprintf(out, "oauth client %s %q: %s\n", c.Id, c.Name, strings.Join(c.RedirectUris, " "))
	}
}
