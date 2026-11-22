package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// memberCommands invite people to an app-tenant.
func memberCommands(command commandFunc) *cobra.Command {
	return group("member", "Invite people to an app-tenant", memberInvite(command))
}

func memberInvite(command commandFunc) *cobra.Command {
	var target recordTarget
	var email, roleKey string
	cmd := command("invite", "Invite a person by email to an app-tenant, with a role", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return nil, nil, err
		}
		var invited InviteMemberInviteMemberApplicationInvitation
		err = s.call(ctx, scope, func(client graphql.Client) error {
			listed, err := roles(ctx, client)
			if err != nil {
				return err
			}
			role, err := roleByKey(listed, roleKey, target)
			if err != nil {
				return err
			}
			response, err := InviteMember(ctx, client, InviteMemberInput{Email: email, RoleIds: []string{role.Id}, Scope: InvitationScopeAppTenant})
			if err == nil {
				invited = response.InviteMember
			}
			return err
		})
		return map[string]any{"invitation": invited}, func(out io.Writer) {
			fmt.Fprintf(out, "Invited %s to %s %s. They get the role when they accept the emailed link.\n", invited.Email, roleKey, where(target))
		}, err
	})
	cmd.Long = "Invite a person by email to an app-tenant, with a role.\n\nA deployer can invite only to app-tenants in environments it created, and is refused a role that can change the model or manage environments."
	appFlag(cmd, &target.appID)
	tenantFlag(cmd, &target.appID, &target.tenantID, nil)
	cmd.Flags().StringVar(&email, "email", "", "the person's email address")
	cmd.Flags().StringVar(&roleKey, "role", "", "the role's key or id (from roles list)")
	_ = cmd.MarkFlagRequired("email")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}
