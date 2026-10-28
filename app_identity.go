package main

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// appIdentityCommands manage the identities an app's own code calls the API as. Each needs
// application.manage_oauth on the application.
func appIdentityCommands(command commandFunc) *cobra.Command {
	return group("app-identity", "Manage the app's own identities for background jobs",
		appIdentityList(command), appIdentityCreate(command), appIdentitySecretGroup(command), appIdentityRetire(command))
}

func appIdentityList(command commandFunc) *cobra.Command {
	var appID string
	cmd := command("list", "List the application's app identities", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		listed := []AppIdentityParts{}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			var err error
			listed, err = appIdentities(ctx, client)
			return err
		})
		return map[string]any{"appIdentities": listed}, func(out io.Writer) {
			table(out, "NAME\tCLIENT ID\tSECRETS\tCREATED", listed, func(a AppIdentityParts) string {
				return fmt.Sprintf("%s\t%s\t%d\t%s", a.Name, a.Id, len(a.Secrets), a.CreatedAt)
			})
		}, err
	})
	appFlag(cmd, &appID)
	return cmd
}

func appIdentityCreate(command commandFunc) *cobra.Command {
	var appID, name string
	cmd := command("create", "Create an app identity and its first secret", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var created AppIdentityParts
		var secret string
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := CreateAppIdentity(ctx, client, CreateAppIdentityInput{Name: name})
			if err == nil {
				created = response.CreateAppIdentity.AppIdentity.AppIdentityParts
				secret = response.CreateAppIdentity.Secret
			}
			return err
		})
		return map[string]any{"appIdentity": created, "secret": secret}, printSecret(created.Id, secret), err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&name, "name", "", "the identity's name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func appIdentitySecretGroup(command commandFunc) *cobra.Command {
	return group("secret", "Add, revoke and list an app identity's secrets",
		appIdentitySecretAdd(command), appIdentitySecretRevoke(command), appIdentitySecretList(command))
}

func appIdentitySecretAdd(command commandFunc) *cobra.Command {
	var appID, clientID string
	cmd := command("add <client-id>", "Add a secret to an app identity", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var identity AppIdentityParts
		var secret string
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := CreateAppIdentitySecret(ctx, client, clientID)
			if err == nil {
				identity = response.CreateAppIdentitySecret.AppIdentity.AppIdentityParts
				secret = response.CreateAppIdentitySecret.Secret
			}
			return err
		})
		return map[string]any{"appIdentity": identity, "secret": secret}, printSecret(identity.Id, secret), err
	})
	cmd.Args = oneID(&clientID)
	appFlag(cmd, &appID)
	return cmd
}

func appIdentitySecretRevoke(command commandFunc) *cobra.Command {
	var appID, secretID string
	cmd := command("revoke <secret-id>", "Revoke one secret; tokens already issued keep working until they expire", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var identity AppIdentityParts
		var lastFour string
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			listed, err := appIdentities(ctx, client)
			if err != nil {
				return err
			}
			for _, candidate := range listed {
				if i := slices.IndexFunc(candidate.Secrets, func(secret AppIdentityPartsSecretsAppIdentitySecret) bool { return secret.Id == secretID }); i >= 0 {
					lastFour = candidate.Secrets[i].LastFour
				}
			}
			if lastFour == "" {
				return &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no app identity secret " + secretID + " in application " + appID}
			}
			response, err := RevokeAppIdentitySecret(ctx, client, secretID)
			if err == nil {
				identity = response.RevokeAppIdentitySecret.AppIdentityParts
			}
			return err
		})
		return map[string]any{"appIdentity": identity}, func(out io.Writer) { fmt.Fprintf(out, "Revoked secret ••••%s\n", lastFour) }, err
	})
	cmd.Args = oneID(&secretID)
	appFlag(cmd, &appID)
	return cmd
}

func appIdentitySecretList(command commandFunc) *cobra.Command {
	var appID, clientID string
	cmd := command("list <client-id>", "List an app identity's secrets", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		secrets := []AppIdentityPartsSecretsAppIdentitySecret{}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			listed, err := appIdentities(ctx, client)
			if err != nil {
				return err
			}
			i := slices.IndexFunc(listed, func(a AppIdentityParts) bool { return a.Id == clientID })
			if i < 0 {
				return &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no app identity " + clientID + " in application " + appID}
			}
			secrets = listed[i].Secrets
			return nil
		})
		return map[string]any{"secrets": secrets}, func(out io.Writer) {
			table(out, "ID\tLAST FOUR\tCREATED\tLAST USED", secrets, func(s AppIdentityPartsSecretsAppIdentitySecret) string {
				lastUsed := "-"
				if s.LastUsedAt != nil {
					lastUsed = *s.LastUsedAt
				}
				return s.Id + "\t" + s.LastFour + "\t" + s.CreatedAt + "\t" + lastUsed
			})
		}, err
	})
	cmd.Args = oneID(&clientID)
	appFlag(cmd, &appID)
	return cmd
}

func appIdentityRetire(command commandFunc) *cobra.Command {
	var appID, clientID string
	cmd := command("retire <client-id>", "Retire an app identity; its tokens stop working", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var retired AppIdentityParts
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := RetireAppIdentity(ctx, client, clientID)
			if err == nil {
				retired = response.RetireAppIdentity.AppIdentityParts
			}
			return err
		})
		return map[string]any{"appIdentity": retired}, func(out io.Writer) {
			fmt.Fprintf(out, "Retired %s; its tokens stop working now.\n", retired.Name)
		}, err
	})
	cmd.Args = oneID(&clientID)
	appFlag(cmd, &appID)
	return cmd
}

func appIdentities(ctx context.Context, client graphql.Client) ([]AppIdentityParts, error) {
	response, err := AppIdentities(ctx, client)
	if err != nil {
		return nil, err
	}
	listed := make([]AppIdentityParts, 0, len(response.AppIdentities))
	for _, item := range response.AppIdentities {
		listed = append(listed, item.AppIdentityParts)
	}
	return listed, nil
}

// oneID takes the command's one positional argument into id.
func oneID(id *string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return err
		}
		*id = args[0]
		return nil
	}
}

func printSecret(clientID, secret string) func(io.Writer) {
	return func(out io.Writer) {
		fmt.Fprintf(out, "Client ID: %s\nSecret:    %s\n\nStore the secret now; it is not shown again.\n", clientID, secret)
	}
}
