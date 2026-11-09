package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// deployerCommands manage the credentials CI uses to create and remove test environments. Each needs
// access.manage.application on the application.
func deployerCommands(command commandFunc) *cobra.Command {
	return group("deployers", "Manage the credentials CI uses to create and remove test environments",
		deployersList(command), deployersCreate(command), deployersRetire(command))
}

func deployersList(command commandFunc) *cobra.Command {
	var appID string
	cmd := command("list", "List the application's deployers", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		listed := []DeployerParts{}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := Deployers(ctx, client, appID)
			if err == nil {
				for _, deployer := range response.Application.Deployers {
					listed = append(listed, deployer.DeployerParts)
				}
			}
			return err
		})
		return map[string]any{"deployers": listed}, func(out io.Writer) {
			table(out, "NAME\tCLIENT ID\tSECRET ENDS\tCREATED", listed, func(d DeployerParts) string {
				return fmt.Sprintf("%s\t%s\t%s\t%s", d.Name, d.Id, d.LastFour, d.CreatedAt)
			})
		}, err
	})
	appFlag(cmd, &appID)
	return cmd
}

func deployersCreate(command commandFunc) *cobra.Command {
	var appID, name string
	cmd := command("create", "Create a deployer and its secret", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var created DeployerParts
		var secret string
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := CreateDeployer(ctx, client, CreateDeployerInput{ApplicationId: appID, Name: name})
			if err == nil {
				created = response.CreateDeployer.Deployer.DeployerParts
				secret = response.CreateDeployer.Secret
			}
			return err
		})
		return map[string]any{"deployer": created, "secret": secret}, printSecret(created.Id, secret), err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&name, "name", "", "the deployer's name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func deployersRetire(command commandFunc) *cobra.Command {
	var appID, id string
	cmd := command("retire", "Retire a deployer; its tokens stop working at once", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		retiredID := id
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := RetireDeployer(ctx, client, id)
			if err == nil {
				retiredID = response.RetireDeployer.Id
			}
			return err
		})
		return map[string]any{"retired": true, "id": retiredID}, func(out io.Writer) { fmt.Fprintf(out, "Retired deployer %s\n", retiredID) }, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&id, "id", "", "the deployer's client ID, from deployers list")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}
