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

// tenantsInvite invites a person by email to an app-tenant with one or more roles. The server emails them a link,
// and they hold the roles once they accept it. It needs access.manage in the app-tenant, and every role given must
// carry only permissions the caller holds there.
func tenantsInvite(command commandFunc) *cobra.Command {
	var target recordTarget
	var email string
	var roleValues []string
	cmd := command("invite", "Invite a person by email to an app-tenant, with one or more roles", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return nil, nil, err
		}
		var invited InvitationParts
		err = s.call(ctx, scope, func(client graphql.Client) error {
			roleIDs, err := grantableRoleIDs(ctx, client, roleValues, target)
			if err != nil {
				return err
			}
			response, err := InviteMember(ctx, client, InviteMemberInput{Email: email, RoleIds: roleIDs, Scope: InvitationScopeAppTenant})
			if err == nil {
				invited = response.InviteMember.InvitationParts
			}
			return err
		})
		return map[string]any{"invitation": invited}, func(out io.Writer) {
			fmt.Fprintf(out, "Invited %s to app-tenant %s with roles %s (invitation %s). They are emailed a link and hold the roles once they accept it, before %s.\n",
				invited.Email, target.tenantID, roleKeys(invited.Roles), invited.Id, invited.ExpiresAt)
		}, accessRefusal(err, target)
	})
	cmd.Long = "Invite a person by email to an app-tenant, with one or more roles. MultiPaaS emails them a link, and they hold the roles once they accept it.\n\n" + accessHelp
	appFlag(cmd, &target.appID)
	tenantFlag(cmd, &target.appID, &target.tenantID, nil)
	cmd.Flags().StringVar(&email, "email", "", "the person's email address")
	roleFlag(cmd, &roleValues)
	_ = cmd.MarkFlagRequired("email")
	return cmd
}

// tenantsMembers lists an app-tenant's members and its pending invitations, email and import alike. The server
// gives a member no roles, and lists only the invitations the caller could resend.
func tenantsMembers(command commandFunc) *cobra.Command {
	var target recordTarget
	cmd := command("members", "List an app-tenant's members and its pending invitations", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return nil, nil, err
		}
		members, err := pages(func(page PageInput) ([]TenantMembersMembersTenantMemberPageItemsTenantMember, bool, error) {
			var listed *TenantMembersResponse
			err := s.call(ctx, scope, func(client graphql.Client) (err error) {
				listed, err = TenantMembers(ctx, client, target.tenantID, page)
				return err
			})
			if err != nil {
				return nil, false, err
			}
			return listed.Members.Items, listed.Members.PageInfo.HasMore, nil
		})
		if err != nil {
			return nil, nil, err
		}
		invitations, err := tenantInvitations(ctx, s, scope, map[string]any{"appTenantId": target.tenantID, "status": "pending"})
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"members": members, "invitations": invitations}, func(out io.Writer) {
			printMembers(out, target.tenantID, members, invitations)
		}, nil
	})
	cmd.Long = "List an app-tenant's members and its pending invitations, email and import alike.\n\n" +
		"A member is listed without its roles. Only the invitations you could resend are listed: with access management (access.manage) in the app-tenant, those whose roles and groups carry only permissions you hold there."
	appFlag(cmd, &target.appID)
	tenantFlag(cmd, &target.appID, &target.tenantID, nil)
	return cmd
}

func printMembers(out io.Writer, tenantID string, members []TenantMembersMembersTenantMemberPageItemsTenantMember, invitations []InvitationParts) {
	if len(members) == 0 {
		fmt.Fprintf(out, "App-tenant %s has no members.\n", tenantID)
	} else {
		fmt.Fprintf(out, "Members of app-tenant %s (their roles are not listed):\n", tenantID)
		table(out, "EMAIL\tNAME\tUSER ID\tSINCE", members, func(m TenantMembersMembersTenantMemberPageItemsTenantMember) string {
			return textOrBlank(m.User.Email) + "\t" + textOrBlank(m.User.Name) + "\t" + m.User.Id + "\t" + m.CreatedAt
		})
	}
	if len(invitations) == 0 {
		fmt.Fprintln(out, "No pending invitations you can manage.")
		return
	}
	fmt.Fprintln(out, "Pending invitations you can manage:")
	table(out, "EMAIL\tROLES\tGROUPS\tKIND\tEXPIRES\tINVITATION ID", invitations, func(i InvitationParts) string {
		return i.Email + "\t" + roleKeys(i.Roles) + "\t" + groupNames(i.Groups) + "\t" + string(i.Kind) + "\t" + i.ExpiresAt + "\t" + i.Id
	})
}

// tenantsInvitationUpdate sets the roles of a pending email invitation to an app-tenant: the roles given replace the
// ones it lists, its groups are kept, and the invitee holds them once they accept.
func tenantsInvitationUpdate(command commandFunc) *cobra.Command {
	var target recordTarget
	var invitationID string
	var roleValues []string
	cmd := command("update", "Set the roles of an invitation that hasn't been accepted; the roles given replace its roles", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return nil, nil, err
		}
		pending, err := tenantInvitations(ctx, s, scope, map[string]any{"appTenantId": target.tenantID, "kind": "email", "status": "pending"})
		if err != nil {
			return nil, nil, accessRefusal(err, target)
		}
		i := slices.IndexFunc(pending, func(p InvitationParts) bool { return p.Id == invitationID })
		if i < 0 {
			return nil, nil, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no pending email invitation " + invitationID + " in app-tenant " + target.tenantID +
				" that you can change: it may be accepted, deleted or expired, or in another app-tenant, or you lack access management there, or it lists a role or group carrying a permission you don't hold there (tenants members lists the ones you can change)"}
		}
		// updateInvitation replaces the groups as well as the roles, so sending the groups just read keeps them. Read
		// then write: a group changed in the web app between the two is overwritten.
		groupIDs := []string{}
		for _, g := range pending[i].Groups {
			groupIDs = append(groupIDs, g.Id)
		}
		var updated InvitationParts
		err = s.call(ctx, scope, func(client graphql.Client) error {
			roleIDs, err := grantableRoleIDs(ctx, client, roleValues, target)
			if err != nil {
				return err
			}
			response, err := UpdateInvitation(ctx, client, UpdateInvitationInput{Id: invitationID, RoleIds: roleIDs, GroupIds: groupIDs})
			if err == nil {
				updated = response.UpdateInvitation.InvitationParts
			}
			return err
		})
		return map[string]any{"invitation": updated}, func(out io.Writer) {
			fmt.Fprintf(out, "Invitation %s for %s in app-tenant %s now gives roles %s; they hold them once they accept.\n", updated.Id, updated.Email, target.tenantID, roleKeys(updated.Roles))
			if len(updated.Groups) > 0 {
				fmt.Fprintf(out, "It keeps its groups: %s.\n", groupNames(updated.Groups))
			}
		}, accessRefusal(err, target)
	})
	cmd.Long = "Set the roles of a pending email invitation to an app-tenant, one that tenants members lists: the roles given replace the ones it lists, and the invitee holds them once they accept. Its groups are kept.\n\n" + updateAccessHelp
	appFlag(cmd, &target.appID)
	tenantFlag(cmd, &target.appID, &target.tenantID, nil)
	cmd.Flags().StringVar(&invitationID, "invitation", "", "the invitation's id (from tenants members)")
	roleFlag(cmd, &roleValues)
	_ = cmd.MarkFlagRequired("invitation")
	return cmd
}

// accessHelp is what tenants invite needs, which the server refuses alike as UNAUTHORIZED.
const accessHelp = "It needs access management (access.manage) in the app-tenant, and every role given must carry only permissions you hold there. The server refuses with UNAUTHORIZED a lack of either."

// updateAccessHelp is what tenants invitation update needs and the refusal each lack gets: the CLI's own NOT_FOUND for an
// invitation it cannot list, the server's UNAUTHORIZED for a new role.
const updateAccessHelp = "It needs access management (access.manage) in the app-tenant. Without it, or when the invitation lists a role or group carrying a permission you don't hold there, the invitation is not listed, and the command refuses with NOT_FOUND: it never sends the update, so nothing changes. Every role given must carry only permissions you hold there. The server refuses with UNAUTHORIZED a role that doesn't."

// roleFlag is --role, repeatable: each a role's key or id, as roles list prints them.
func roleFlag(cmd *cobra.Command, values *[]string) {
	cmd.Flags().StringArrayVar(values, "role", nil, "a role's key or id (from roles list); repeat for each role")
	_ = cmd.MarkFlagRequired("role")
}

// grantableRoleIDs resolves each value as roleByKey does, among the roles the target app-tenant can grant, each role once.
func grantableRoleIDs(ctx context.Context, client graphql.Client, values []string, target recordTarget) ([]string, error) {
	listed, err := roles(ctx, client)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, value := range values {
		role, err := roleByKey(listed, value, target)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(ids, role.Id) {
			ids = append(ids, role.Id)
		}
	}
	return ids, nil
}

// accessRefusal says what an UNAUTHORIZED from inviting or changing an invitation can mean. The server answers a caller
// without access management and one giving a role with a permission the caller lacks with the same code and message.
// For a person, an app-tenant they can't reach never gets this far: appScope refuses it with NOT_FOUND first.
func accessRefusal(err error, target recordTarget) error {
	if err == nil {
		return nil
	}
	failure := asCLIError(err)
	if failure.Code != "UNAUTHORIZED" {
		return err
	}
	hinted := *failure
	hinted.Message += ": in app-tenant " + target.tenantID + " you need access management, and every role given must carry only permissions you hold there"
	return &hinted
}

// roleKeys is an invitation's role keys, comma-separated, for a line of output.
func roleKeys(roles []InvitationPartsRolesRole) string {
	keys := []string{}
	for _, r := range roles {
		keys = append(keys, r.Key)
	}
	return strings.Join(keys, ", ")
}

// groupNames is an invitation's group names, comma-separated, for a line of output.
func groupNames(groups []InvitationPartsGroupsAccessGroup) string {
	names := []string{}
	for _, g := range groups {
		names = append(names, g.Name)
	}
	return strings.Join(names, ", ")
}

// tenantInvitations is every invitation that matches where and that the caller could resend.
func tenantInvitations(ctx context.Context, s *session, scope string, where map[string]any) ([]InvitationParts, error) {
	return pages(func(page PageInput) ([]InvitationParts, bool, error) {
		var listed *TenantInvitationsResponse
		err := s.call(ctx, scope, func(client graphql.Client) (err error) {
			listed, err = TenantInvitations(ctx, client, page, where)
			return err
		})
		if err != nil {
			return nil, false, err
		}
		found := []InvitationParts{}
		for _, item := range listed.Invitations.Items {
			found = append(found, item.InvitationParts)
		}
		return found, listed.Invitations.PageInfo.HasMore, nil
	})
}

// textOrBlank is a nullable text for a table cell.
func textOrBlank(text *string) string {
	if text == nil {
		return ""
	}
	return *text
}
