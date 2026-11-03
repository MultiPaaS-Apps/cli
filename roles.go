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

// roleCommands declare, change and list the application's roles. Creating or changing one needs access.manage.application.
func roleCommands(command commandFunc) *cobra.Command {
	return group("roles", "Declare, change and list the application's roles",
		roleList(command), rolePermissions(command), roleCreate(command), roleUpdate(command))
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
		parsed, err := parsePermissions("--permission", permissions)
		if err != nil {
			return nil, nil, err
		}
		input := CreateRoleInput{Key: key, Name: name, Permissions: parsed}
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

func roleUpdate(command commandFunc) *cobra.Command {
	var appID, roleKey, name string
	var added, removed []string
	var cmd *cobra.Command
	cmd = command("update", "Change an application role's permissions or name; what is not given is kept", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		renamed := cmd.Flags().Changed("name")
		if len(added) == 0 && len(removed) == 0 && !renamed {
			return nil, nil, fmt.Errorf("give --add-permission, --remove-permission or --name")
		}
		if renamed && name == "" {
			return nil, nil, fmt.Errorf("--name is empty")
		}
		adds, err := parsePermissions("--add-permission", added)
		if err != nil {
			return nil, nil, err
		}
		removes, err := parsePermissions("--remove-permission", removed)
		if err != nil {
			return nil, nil, err
		}
		target := recordTarget{appID: appID, appWide: true}
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return nil, nil, err
		}
		var updated RoleParts
		err = s.call(ctx, scope, func(client graphql.Client) error {
			listed, err := roles(ctx, client)
			if err != nil {
				return err
			}
			role, err := roleByKey(listed, roleKey, target)
			if err != nil {
				if refusal, ok := err.(*cliError); ok && refusal.Code == "NOT_FOUND" {
					return &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no role " + roleKey + " in application " + appID}
				}
				return err
			}
			input := UpdateRoleInput{Id: role.Id}
			if renamed {
				input.Name = &name
			}
			if len(adds) > 0 || len(removes) > 0 {
				if input.Permissions, err = nextPermissions(role, adds, removes); err != nil {
					return err
				}
			}
			response, err := UpdateRole(ctx, client, input)
			if err == nil {
				updated = response.UpdateRole.RoleParts
			}
			return err
		})
		return map[string]any{"role": updated}, func(out io.Writer) { fmt.Fprintf(out, "Updated role %s (%s)\n", updated.Key, updated.Id) }, err
	})
	appFlag(cmd, &appID)
	cmd.Flags().StringVar(&roleKey, "role", "", "the role's key or id (from roles list); an application role")
	cmd.Flags().StringArrayVar(&added, "add-permission", nil, "a permission to add, as PERMISSION or PERMISSION=ENTITY_ID; repeat for each (from roles permissions)")
	cmd.Flags().StringArrayVar(&removed, "remove-permission", nil, "a permission to remove, written as roles list shows it; repeat for each")
	cmd.Flags().StringVar(&name, "name", "", "the role's new name, which memberships.jsonl names it by")
	_ = cmd.MarkFlagRequired("role")
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

// parsePermissions reads each PERMISSION or PERMISSION=ENTITY_ID given to flag, the form roles list prints.
func parsePermissions(flag string, given []string) ([]RolePermissionInput, error) {
	parsed := []RolePermissionInput{}
	for _, value := range given {
		permission, entityID, targeted := strings.Cut(value, "=")
		if permission == "" {
			return nil, fmt.Errorf("%s %q names no permission", flag, value)
		}
		if targeted && entityID == "" {
			return nil, fmt.Errorf("%s %q names no entity id", flag, value)
		}
		parsed = append(parsed, RolePermissionInput{Permission: permission, EntityId: entityID})
	}
	return parsed, nil
}

// nextPermissions is the role's permission set with removes taken out, then adds put in. Removing a permission
// the role doesn't carry is refused, so a typo isn't a silent no-op. Adding one it already carries changes nothing.
func nextPermissions(role RoleParts, adds, removes []RolePermissionInput) ([]RolePermissionInput, error) {
	next := []RolePermissionInput{}
	for _, p := range role.Permissions {
		carried := RolePermissionInput{Permission: p.Permission}
		if p.EntityId != nil {
			carried.EntityId = *p.EntityId
		}
		next = append(next, carried)
	}
	for _, p := range removes {
		i := slices.IndexFunc(next, func(c RolePermissionInput) bool { return samePermission(c, p) })
		if i < 0 {
			return nil, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "role " + role.Key + " doesn't carry " + permissionText(p)}
		}
		next = slices.Delete(next, i, i+1)
	}
	for _, p := range adds {
		if !slices.ContainsFunc(next, func(c RolePermissionInput) bool { return samePermission(c, p) }) {
			next = append(next, p)
		}
	}
	return next, nil
}

// samePermission is whether a and b name the same permission; entity ids are uuids, which compare without case.
func samePermission(a, b RolePermissionInput) bool {
	return a.Permission == b.Permission && strings.EqualFold(a.EntityId, b.EntityId)
}

func permissionText(p RolePermissionInput) string {
	if p.EntityId == "" {
		return p.Permission
	}
	return p.Permission + "=" + p.EntityId
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
	scopeFlags(cmd, &target, "list the roles held across the application instead of in an app-tenant", true)
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
	scopeFlags(cmd, &target, "give the role across the application instead of in an app-tenant", true)
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
	scopeFlags(cmd, &target, "remove a role held across the application instead of in an app-tenant", true)
	cmd.Flags().StringVar(&roleKey, "role", "", "the role's key or id (from roles list)")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

// inGrantScope runs operation in the target's scope, with the app identity clientID names and the role
// assignments it holds in exactly that scope. The identity is looked up in an app-tenant's own scope for an
// app-tenant target, because the server lists the identities of that app-tenant's environment there. The server can show an app-tenant's read the application's
// grants as well, so they are filtered out here.
func (s *session) inGrantScope(ctx context.Context, target recordTarget, clientID string, operation func(graphql.Client, AppIdentityParts, []RoleAssignmentParts) error) error {
	var identity AppIdentityParts
	lookup := func(operation func(graphql.Client) error) error {
		if target.appWide || target.tenantID == "" {
			return s.inEnv(ctx, target.appID, target.envID, operation)
		}
		scope, err := s.recordScope(ctx, target)
		if err != nil {
			return err
		}
		return s.call(ctx, scope, operation)
	}
	err := lookup(func(client graphql.Client) error {
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
			if heldIn(item.AppTenant, target) {
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
