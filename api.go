package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// maxPageSize is the largest page the server returns.
const maxPageSize = 100

// signedIn is who whoami and login report.
type signedIn struct {
	User    MeMeUser `json:"user"`
	Profile *string  `json:"profile"`
}

// app is an application the developer can reach, and the client that owns it.
type app struct {
	ClientID   string `json:"clientId"`
	ClientName string `json:"clientName"`
	ID         string `json:"id"`
	Name       string `json:"name"`
}

type appTenant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *session) whoami(ctx context.Context) (signedIn, error) {
	var me *MeResponse
	err := s.call(ctx, "", func(client graphql.Client) (err error) {
		me, err = Me(ctx, client)
		return err
	})
	if err != nil {
		return signedIn{}, err
	}
	return signedIn{User: me.Me, Profile: s.profileJSON()}, nil
}

// apps lists every application the developer's entitlements reach, each once.
func (s *session) apps(ctx context.Context) ([]app, error) {
	entitlements, err := s.entitlements(ctx)
	if err != nil {
		return nil, err
	}
	apps := []app{}
	for _, entitlement := range entitlements {
		reached, err := s.appsIn(ctx, entitlement)
		if err != nil {
			return nil, err
		}
		for _, candidate := range reached {
			if !slices.ContainsFunc(apps, func(known app) bool { return known.ID == candidate.ID }) {
				apps = append(apps, candidate)
			}
		}
	}
	return apps, nil
}

// tenants lists the app-tenants of appID the developer's entitlements reach, each once.
func (s *session) tenants(ctx context.Context, appID string) ([]appTenant, error) {
	entitlements, err := s.entitlements(ctx)
	if err != nil {
		return nil, err
	}
	tenants := []appTenant{}
	found := false
	for _, entitlement := range entitlements {
		reached, ok, err := s.tenantsIn(ctx, entitlement, appID)
		if err != nil {
			return nil, err
		}
		found = found || ok
		for _, candidate := range reached {
			if !slices.Contains(tenants, candidate) {
				tenants = append(tenants, candidate)
			}
		}
	}
	if !found {
		return nil, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no application " + appID + " that you can reach"}
	}
	return tenants, nil
}

// creatableClient is the client a new application goes in: clientID when given, else the developer's only
// client-level entitlement. An app-tenant-level entitlement reaches a single app-tenant, never its client.
func (s *session) creatableClient(ctx context.Context, clientID string) (MyTenantsMyTenantsTenantOptionClient, error) {
	entitlements, err := s.entitlements(ctx)
	if err != nil {
		return MyTenantsMyTenantsTenantOptionClient{}, err
	}
	clients := []MyTenantsMyTenantsTenantOptionClient{}
	for _, entitlement := range entitlements {
		if entitlement.AppTenantId == "" && (clientID == "" || entitlement.Client.Id == clientID) && !slices.Contains(clients, entitlement.Client) {
			clients = append(clients, entitlement.Client)
		}
	}
	switch {
	case len(clients) == 1:
		return clients[0], nil
	case len(clients) == 0 && clientID != "":
		return MyTenantsMyTenantsTenantOptionClient{}, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no client " + clientID + " you can create applications in"}
	case len(clients) == 0:
		return MyTenantsMyTenantsTenantOptionClient{}, &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "no client you can create applications in"}
	}
	named := []string{}
	for _, client := range clients {
		named = append(named, client.Id+" ("+client.Name+")")
	}
	return MyTenantsMyTenantsTenantOptionClient{}, &cliError{exit: exitRefused, Code: "CLIENT_REQUIRED", Message: "you can reach more than one client: pass --client with one of " + strings.Join(named, ", ")}
}

// entitlements is myTenants: where the developer may act, with no scope sent.
func (s *session) entitlements(ctx context.Context) ([]MyTenantsMyTenantsTenantOption, error) {
	var mine *MyTenantsResponse
	err := s.call(ctx, "", func(client graphql.Client) (err error) {
		mine, err = MyTenants(ctx, client)
		return err
	})
	if err != nil {
		return nil, err
	}
	return mine.MyTenants, nil
}

// appsIn lists what one entitlement reaches: every app of a client-level entitlement, or the one app
// an app-tenant-level entitlement belongs to (a client-only scope is not entitled for it).
func (s *session) appsIn(ctx context.Context, entitlement MyTenantsMyTenantsTenantOption) ([]app, error) {
	owned := func(id, name string) app {
		return app{ClientID: entitlement.Client.Id, ClientName: entitlement.Client.Name, ID: id, Name: name}
	}
	if entitlement.AppTenantId != "" {
		scoped, err := s.scopedAppTenant(ctx, entitlement)
		return []app{owned(scoped.Application.Id, scoped.Application.Name)}, err
	}
	return pages(func(page PageInput) ([]app, bool, error) {
		var listed *ApplicationsResponse
		err := s.call(ctx, entitlement.Client.Id, func(client graphql.Client) (err error) {
			listed, err = Applications(ctx, client, page)
			return err
		})
		if err != nil {
			return nil, false, err
		}
		reached := []app{}
		for _, item := range listed.Applications.Items {
			reached = append(reached, owned(item.Id, item.Name))
		}
		return reached, listed.Applications.PageInfo.HasMore, nil
	})
}

// tenantsIn lists appID's app-tenants one entitlement reaches, and whether it reaches appID at all.
func (s *session) tenantsIn(ctx context.Context, entitlement MyTenantsMyTenantsTenantOption, appID string) ([]appTenant, bool, error) {
	if entitlement.AppTenantId != "" {
		if entitlement.ApplicationId != appID {
			return nil, false, nil
		}
		scoped, err := s.scopedAppTenant(ctx, entitlement)
		return []appTenant{{ID: scoped.AppTenant.Id, Name: scoped.AppTenant.Name}}, true, err
	}
	apps, err := s.appsIn(ctx, entitlement)
	if err != nil || !slices.ContainsFunc(apps, func(candidate app) bool { return candidate.ID == appID }) {
		return nil, false, err
	}
	tenants, err := s.tenantsAt(ctx, entitlement.Client.Id+"/"+appID)
	return tenants, true, err
}

// tenantsAt lists the app-tenants scope reaches: every one of an application, or one environment's.
func (s *session) tenantsAt(ctx context.Context, scope string) ([]appTenant, error) {
	return pages(func(page PageInput) ([]appTenant, bool, error) {
		var listed *AppTenantsResponse
		err := s.call(ctx, scope, func(client graphql.Client) (err error) {
			listed, err = AppTenants(ctx, client, page)
			return err
		})
		if err != nil {
			return nil, false, err
		}
		reached := []appTenant{}
		for _, item := range listed.AppTenants.Items {
			reached = append(reached, appTenant{ID: item.Id, Name: item.Name})
		}
		return reached, listed.AppTenants.PageInfo.HasMore, nil
	})
}

// scopedAppTenant reads the app and app-tenant an app-tenant-level entitlement names, in its own scope.
func (s *session) scopedAppTenant(ctx context.Context, entitlement MyTenantsMyTenantsTenantOption) (ScopedAppTenantMeUser, error) {
	var scoped *ScopedAppTenantResponse
	scope := entitlement.Client.Id + "/" + entitlement.ApplicationId + "/" + entitlement.AppTenantId
	err := s.call(ctx, scope, func(client graphql.Client) (err error) {
		scoped, err = ScopedAppTenant(ctx, client)
		return err
	})
	if err != nil {
		return ScopedAppTenantMeUser{}, err
	}
	return scoped.Me, nil
}

// call runs one webapp-api operation as the developer, in scope ("" for none). An UNAUTHORIZED
// answer may only mean the token died early, so it refreshes once and retries; a second
// UNAUTHORIZED is a real refusal.
func (s *session) call(ctx context.Context, scope string, operation func(graphql.Client) error) error {
	token, err := s.token(ctx, "")
	if err != nil {
		return err
	}
	err = fromGraphQL(operation(s.client(token, scope)))
	var refusal *cliError
	if !errors.As(err, &refusal) || refusal.Code != "UNAUTHORIZED" {
		return err
	}
	if token, err = s.token(ctx, token); err != nil {
		return err
	}
	return fromGraphQL(operation(s.client(token, scope)))
}

// client sends the bearer, the CLI version, and the scope (when there is one) on every request.
func (s *session) client(token, scope string) graphql.Client {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	header.Set("X-MultiPaaS-Cli-Version", s.env.version)
	if scope != "" {
		header.Set("X-MultiPaaS-Scope", scope)
	}
	return graphql.NewClient(s.origins.webappAPI+"/graphql", withHeader{doer: s.http, header: header})
}

type withHeader struct {
	doer   *http.Client
	header http.Header
}

func (d withHeader) Do(request *http.Request) (*http.Response, error) {
	for name, values := range d.header {
		request.Header[name] = values
	}
	return d.doer.Do(request)
}

// fromGraphQL gives a webapp-api refusal its exit code: UNSUPPORTED_CLI_VERSION is 3, any other code is 1.
// A VALIDATION refusal also carries the fields it names.
func fromGraphQL(err error) error {
	var refusals gqlerror.List
	if !errors.As(err, &refusals) || len(refusals) == 0 {
		return err
	}
	code, _ := refusals[0].Extensions["code"].(string)
	if code == "" {
		return err
	}
	exit := exitRefused
	if code == "UNSUPPORTED_CLI_VERSION" {
		exit = exitUpgrade
	}
	failure := &cliError{exit: exit, Code: code, Message: refusals[0].Message}
	failure.FieldErrors = fieldErrorsOf(refusals[0].Extensions["fieldErrors"])
	return failure
}

// fieldErrorsOf reads a refusal's fieldErrors extension, which arrives as untyped JSON; one that does not decode is dropped.
func fieldErrorsOf(extension any) []fieldError {
	data, err := json.Marshal(extension)
	if err != nil {
		return nil
	}
	var fields []fieldError
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil
	}
	return fields
}

// pages collects every page of a paginated query.
func pages[T any](fetch func(PageInput) ([]T, bool, error)) ([]T, error) {
	all := []T{}
	for page := 0; ; page++ {
		items, hasMore, err := fetch(PageInput{Page: page, PageSize: maxPageSize})
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if !hasMore {
			return all, nil
		}
	}
}
