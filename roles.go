package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// roleCommands declare the application's roles. Creating one needs access.manage.application.
func roleCommands(command commandFunc) *cobra.Command {
	return group("roles", "Declare and list the application's roles",
		roleList(command), rolePermissions(command), roleCreate(command))
}

func roleList(command commandFunc) *cobra.Command {
	var appID, tenantID string
	cmd := command("list", "List the application's roles", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		listed := []RoleParts{}
		read := func(client graphql.Client) error {
			var err error
			listed, err = roles(ctx, client)
			return err
		}
		var err error
		if tenantID == "" {
			err = s.inApp(ctx, appID, read)
		} else {
			var scope string
			if scope, err = s.recordScope(ctx, recordTarget{appID: appID, tenantID: tenantID}); err == nil {
				err = s.call(ctx, scope, read)
			}
		}
		return map[string]any{"roles": listed}, func(out io.Writer) {
			table(out, "KEY\tNAME\tSCOPE\tPERMISSIONS\tID", listed, func(r RoleParts) string {
				scope := "application"
				if r.AppTenant != nil {
					scope = "app-tenant " + r.AppTenant.Id
				}
				return r.Key + "\t" + r.Name + "\t" + scope + "\t" + permissionList(r.Permissions) + "\t" + r.Id
			})
		}, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&tenantID, "tenant", "", "also list this app-tenant's own roles (an app-tenant id from tenants list)")
	return cmd
}

func rolePermissions(command commandFunc) *cobra.Command {
	var appID string
	cmd := command("permissions", "List the permissions a role can carry", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		listed := []PermissionCatalogPermissionCatalogPermissionInfo{}
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			response, err := PermissionCatalog(ctx, client)
			if err == nil {
				listed = response.PermissionCatalog
			}
			return err
		})
		return map[string]any{"permissions": listed}, func(out io.Writer) {
			table(out, "PERMISSION\tLEVEL\tTAKES ENTITY", listed, func(p PermissionCatalogPermissionCatalogPermissionInfo) string {
				return p.Key + "\t" + p.Level + "\t" + strconv.FormatBool(p.EntityTargeted)
			})
		}, err
	})
	appFlag(cmd, &appID)
	return cmd
}

func roleCreate(command commandFunc) *cobra.Command {
	var appID, key, name string
	var permissions []string
	cmd := command("create", "Declare a role across the application", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		input := CreateRoleInput{Key: key, Name: name, Permissions: []RolePermissionInput{}}
		for _, given := range permissions {
			permission, entityID, _ := strings.Cut(given, "=")
			if permission == "" {
				return nil, nil, fmt.Errorf("--permission %q names no permission", given)
			}
			input.Permissions = append(input.Permissions, RolePermissionInput{Permission: permission, EntityId: entityID})
		}
		scope, err := s.recordScope(ctx, recordTarget{appID: appID, appWide: true})
		if err != nil {
			return nil, nil, err
		}
		var created RoleParts
		err = s.call(ctx, scope, func(client graphql.Client) error {
			response, err := CreateRole(ctx, client, input)
			if err == nil {
				created = response.CreateRole.RoleParts
			}
			return err
		})
		return map[string]any{"role": created}, func(out io.Writer) { fmt.Fprintf(out, "Created role %s (%s)\n", created.Key, created.Id) }, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&key, "key", "", "the role's key, unique among the application's roles")
	cmd.Flags().StringVar(&name, "name", "", "the role's name, which memberships.jsonl names it by")
	cmd.Flags().StringArrayVar(&permissions, "permission", nil, "a permission the role carries, as PERMISSION or PERMISSION=ENTITY_ID; repeat for each (from roles permissions)")
	_ = cmd.MarkFlagRequired("key")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func roles(ctx context.Context, client graphql.Client) ([]RoleParts, error) {
	response, err := Roles(ctx, client)
	if err != nil {
		return nil, err
	}
	listed := make([]RoleParts, 0, len(response.Roles))
	for _, item := range response.Roles {
		listed = append(listed, item.RoleParts)
	}
	return listed, nil
}

// permissionList is a role's permissions as PERMISSION or PERMISSION=ENTITY_ID, the form --permission takes.
func permissionList(permissions []RolePartsPermissionsRolePermission) string {
	given := make([]string, 0, len(permissions))
	for _, p := range permissions {
		if p.EntityId != nil {
			given = append(given, p.Permission+"="+*p.EntityId)
		} else {
			given = append(given, p.Permission)
		}
	}
	return strings.Join(given, ",")
}

// appIdentityRoleGroup manages the roles an app identity holds in one app-tenant, or with --app-wide across the
// application. Giving and removing one needs access.manage there, or access.manage.application for --app-wide.
func appIdentityRoleGroup(command commandFunc) *cobra.Command {
	return group("role", "Give, remove and list an app identity's roles",
		appIdentityRoleList(command), appIdentityRoleAdd(command), appIdentityRoleRemove(command))
}

func appIdentityRoleList(command commandFunc) *cobra.Command {
	var target recordTarget
	var clientID string
	cmd := command("list <client-id>", "List the roles an app identity holds in the scope; shown only to someone who can manage access there", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		held := []RoleAssignmentParts{}
		err := s.inGrantScope(ctx, target, clientID, func(_ graphql.Client, _ AppIdentityParts, here []RoleAssignmentParts) error {
			held = here
			return nil
		})
		return map[string]any{"assignments": held}, func(out io.Writer) {
			table(out, "ROLE\tNAME\tROLE ID\tASSIGNMENT ID", held, func(a RoleAssignmentParts) string {
				if a.Role == nil {
					return "-\t-\t-\t" + a.Id
				}
				return a.Role.Key + "\t" + a.Role.Name + "\t" + a.Role.Id + "\t" + a.Id
			})
		}, err
	})
	cmd.Args = oneID(&clientID)
	scopeFlags(cmd, &target, "list the roles held across the application instead of in an app-tenant")
	return cmd
}

func appIdentityRoleAdd(command commandFunc) *cobra.Command {
	var target recordTarget
	var clientID, roleKey string
	cmd := command("add <client-id>", "Give an app identity a role in the scope", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var given RoleAssignmentParts
		var name string
		err := s.inGrantScope(ctx, target, clientID, func(client graphql.Client, identity AppIdentityParts, _ []RoleAssignmentParts) error {
			name = identity.Name
			listed, err := roles(ctx, client)
			if err != nil {
				return err
			}
			role, err := roleByKey(listed, roleKey, target)
			if err != nil {
				return err
			}
			response, err := AssignRole(ctx, client, role.Id, identity.User.Id)
			if err == nil {
				given = response.AssignRole.RoleAssignmentParts
			}
			return err
		})
		return map[string]any{"assignment": given}, func(out io.Writer) {
			fmt.Fprintf(out, "Gave %s the role %s %s.\n", name, roleKey, where(target))
		}, err
	})
	cmd.Args = oneID(&clientID)
	scopeFlags(cmd, &target, "give the role across the application instead of in an app-tenant")
	cmd.Flags().StringVar(&roleKey, "role", "", "the role's key or id (from roles list)")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func appIdentityRoleRemove(command commandFunc) *cobra.Command {
	var target recordTarget
	var clientID, roleKey string
	cmd := command("remove <client-id>", "Remove a role an app identity holds in the scope", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var removed RoleAssignmentParts
		var name string
		err := s.inGrantScope(ctx, target, clientID, func(client graphql.Client, identity AppIdentityParts, here []RoleAssignmentParts) error {
			name = identity.Name
			notHeld := &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "app identity " + clientID + " holds no role " + roleKey + " " + where(target) + ", or you can't manage access there to see its roles"}
			listed, err := roles(ctx, client)
			if err != nil {
				return err
			}
			role, err := roleByKey(listed, roleKey, target)
			if err != nil {
				if refusal, ok := err.(*cliError); ok && refusal.Code == "NOT_FOUND" {
					return notHeld
				}
				return err
			}
			i := slices.IndexFunc(here, func(a RoleAssignmentParts) bool { return a.Role != nil && a.Role.Id == role.Id })
			if i < 0 {
				return notHeld
			}
			response, err := UnassignRole(ctx, client, here[i].Id)
			if err == nil {
				removed = response.UnassignRole.RoleAssignmentParts
			}
			return err
		})
		return map[string]any{"assignment": removed}, func(out io.Writer) {
			fmt.Fprintf(out, "Removed the role %s from %s %s.\n", roleKey, name, where(target))
		}, err
	})
	cmd.Args = oneID(&clientID)
	scopeFlags(cmd, &target, "remove a role held across the application instead of in an app-tenant")
	cmd.Flags().StringVar(&roleKey, "role", "", "the role's key or id (from roles list)")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

// inGrantScope runs operation in the target's scope, with the app identity clientID names and the role
// assignments it holds in exactly that scope. The server can show an app-tenant's read the application's
// grants as well, so they are filtered out here.
func (s *session) inGrantScope(ctx context.Context, target recordTarget, clientID string, operation func(graphql.Client, AppIdentityParts, []RoleAssignmentParts) error) error {
	var identity AppIdentityParts
	err := s.inApp(ctx, target.appID, func(client graphql.Client) error {
		listed, err := appIdentities(ctx, client)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(listed, func(a AppIdentityParts) bool { return a.Id == clientID })
		if i < 0 || listed[i].User == nil {
			return &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no app identity " + clientID + " in application " + target.appID}
		}
		identity = listed[i]
		return nil
	})
	if err != nil {
		return err
	}
	scope, err := s.recordScope(ctx, target)
	if err != nil {
		return err
	}
	return s.call(ctx, scope, func(client graphql.Client) error {
		response, err := RoleAssignments(ctx, client, identity.User.Id)
		if err != nil {
			return err
		}
		here := []RoleAssignmentParts{}
		for _, item := range response.RoleAssignments {
			if heldIn(item.RoleAssignmentParts.AppTenant, target) {
				here = append(here, item.RoleAssignmentParts)
			}
		}
		return operation(client, identity, here)
	})
}

// roleByKey finds the role value names among those the target's scope can grant: the application's roles, and
// the target app-tenant's own roles unless the target is app-wide. value is a role id or a key. An application
// role and an app-tenant role can share a key, which is refused as ambiguous.
func roleByKey(listed []RoleParts, value string, target recordTarget) (RoleParts, error) {
	grantable := []RoleParts{}
	for _, r := range listed {
		if r.AppTenant == nil || (!target.appWide && r.AppTenant.Id == target.tenantID) {
			grantable = append(grantable, r)
		}
	}
	if i := slices.IndexFunc(grantable, func(r RoleParts) bool { return r.Id == value }); i >= 0 {
		return grantable[i], nil
	}
	matches := slices.DeleteFunc(slices.Clone(grantable), func(r RoleParts) bool { return r.Key != value })
	switch len(matches) {
	case 0:
		return RoleParts{}, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no role " + value + " in application " + target.appID + " to give " + where(target)}
	case 1:
		return matches[0], nil
	}
	applicationRole, tenantRole := matches[0], matches[1]
	if applicationRole.AppTenant != nil {
		applicationRole, tenantRole = tenantRole, applicationRole
	}
	return RoleParts{}, &cliError{exit: exitRefused, Code: "AMBIGUOUS_ROLE", Message: "role key " + value + " names both the application role " + applicationRole.Id +
		" and app-tenant " + target.tenantID + "'s role " + tenantRole.Id + "; pass --role with the role's id"}
}

// heldIn reports whether an assignment's app-tenant is exactly the target's scope.
func heldIn(appTenant *RoleAssignmentPartsAppTenant, target recordTarget) bool {
	if appTenant == nil {
		return target.appWide
	}
	return !target.appWide && appTenant.Id == target.tenantID
}

func where(target recordTarget) string {
	if target.appWide {
		return "across the application"
	}
	return "in app-tenant " + target.tenantID
}
