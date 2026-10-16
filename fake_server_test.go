package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMultiPaaS stands in for MultiPaaS's OAuth server and webapp-api. It matches the server along the
// dimensions the CLI's defects live in: the authorize, token and revoke rules, and the order of
// webapp-api's checks.
type fakeMultiPaaS struct {
	t      *testing.T
	server *httptest.Server

	mu           sync.Mutex
	deny         bool                   // authorize redirects with error=access_denied
	accessTTL    int                    // expires_in for every minted pair
	codes        map[string]pendingCode // code -> what it was issued for
	chains       map[string]*chain      // any token ever minted -> its chain
	refreshes    int                    // refresh grants answered
	revokes      []url.Values           // revoke forms received
	entitlements []entitlement          // what myTenants answers
	apps         map[string][]fakeRow   // client id -> its apps
	tenants      map[string][]fakeRow   // app id -> its app-tenants
	scopedApps   map[string]fakeRow     // app-tenant id -> its app
	scopes       []string               // X-MultiPaaS-Scope per GraphQL request ("" when absent)
	refused      map[string]bool        // client ids myTenants lists but the scope check refuses
	nameless     bool                   // the signed-in user has no name
	hangToken    bool                   // the token endpoint never answers
	redirectTo   string                 // the token endpoint answers 307 to this URL
	failRevoke   bool                   // the revoke endpoint answers HTTP 500
	release      chan struct{}          // closed at cleanup, to free handlers that never answer
	accessTokens map[string]bool        // every access token minted, so a refresh grant can refuse one

	entities      map[string][]map[string]any // app id -> its entities, as model show reads them
	fields        map[string][]map[string]any // entity id -> its fields
	enumTypes     map[string][]map[string]any // app id -> its enum types
	constraints   map[string][]map[string]any // entity id -> its constraints
	revisions     map[string]int              // app id -> how many revisions its model has
	mutations     []map[string]any            // the variables of every model mutation received
	noModelEdit   bool                        // the signed-in user lacks model.edit
	refuseChange  [2]string                   // a model mutation is refused with this code and message
	failVersion   bool                        // LatestModelRevision answers HTTP 500
	records       map[string][]map[string]any // entity id -> its live records, newest first
	oauthClients  []map[string]any            // the application's sign-in clients
	noManageOAuth bool                        // the signed-in user lacks application.manage_oauth
	noManageApp   bool                        // the signed-in user lacks access.manage.application
}

type pendingCode struct{ redirectURI, challenge string }

// chain is one sign-in's rotation chain: the live pair, every refresh token already used, and whether it is revoked.
type chain struct {
	access, refresh string
	used            map[string]bool
	revoked         bool
	deadAccess      bool // the access token is refused although the chain is live
}

type entitlement struct{ clientID, clientName, applicationID, appTenantID string }

type fakeRow struct{ id, name string }

func newFakeMultiPaaS(t *testing.T) *fakeMultiPaaS {
	fake := &fakeMultiPaaS{
		t: t, accessTTL: 3600,
		codes: map[string]pendingCode{}, chains: map[string]*chain{},
		apps: map[string][]fakeRow{}, tenants: map[string][]fakeRow{}, scopedApps: map[string]fakeRow{}, refused: map[string]bool{},
		release: make(chan struct{}), accessTokens: map[string]bool{},
		entities: map[string][]map[string]any{}, fields: map[string][]map[string]any{}, enumTypes: map[string][]map[string]any{}, constraints: map[string][]map[string]any{}, revisions: map[string]int{},
		records: map[string][]map[string]any{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/authorize", fake.authorize)
	mux.HandleFunc("POST /oauth/token", fake.token)
	mux.HandleFunc("POST /oauth/revoke", fake.revoke)
	mux.HandleFunc("POST /graphql", fake.graphql)
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	t.Cleanup(func() { close(fake.release) })
	return fake
}

func (f *fakeMultiPaaS) origins(string) (origins, error) {
	return origins{api: f.server.URL, webappAPI: f.server.URL}, nil
}

// authorize mirrors the server's authorize endpoint plus an instant consent: a bad client or redirect is a 400 with no
// redirect; otherwise the browser is sent back to the loopback with a code (or access_denied) and the state.
func (f *fakeMultiPaaS) authorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	redirectURI := query.Get("redirect_uri")
	if query.Get("client_id") != developerClientID || !developerRedirect.MatchString(redirectURI) {
		http.Error(w, "Invalid client_id or redirect_uri.", http.StatusBadRequest)
		return
	}
	back, _ := url.Parse(redirectURI)
	answer := url.Values{"state": {query.Get("state")}}
	f.mu.Lock()
	switch {
	case query.Get("response_type") != "code":
		answer.Set("error", "unsupported_response_type")
	case !codeChallenge.MatchString(query.Get("code_challenge")) || query.Get("code_challenge_method") != "S256":
		answer.Set("error", "invalid_request")
	case f.deny:
		answer.Set("error", "access_denied")
	default:
		code := randomToken()
		f.codes[code] = pendingCode{redirectURI: redirectURI, challenge: query.Get("code_challenge")}
		answer.Set("code", code)
	}
	f.mu.Unlock()
	back.RawQuery = answer.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

// token mirrors the server's token endpoint: form-encoded only; a code is single-use and bound to its redirect and
// verifier; a refresh rotates the pair, and a reused refresh token revokes the whole chain.
func (f *fakeMultiPaaS) token(w http.ResponseWriter, r *http.Request) {
	form, ok := f.form(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	if f.hangToken {
		f.mu.Unlock()
		<-f.release
		return
	}
	if f.redirectTo != "" {
		f.mu.Unlock()
		http.Redirect(w, r, f.redirectTo, http.StatusTemporaryRedirect)
		return
	}
	defer f.mu.Unlock()
	switch form.Get("grant_type") {
	case "authorization_code":
		pending, ok := f.codes[form.Get("code")]
		delete(f.codes, form.Get("code"))
		if !ok || pending.redirectURI != form.Get("redirect_uri") || challengeFor(form.Get("code_verifier")) != pending.challenge {
			oauthRefusal(w, "invalid_grant", "The authorization code is invalid.")
			return
		}
		f.mint(w, &chain{used: map[string]bool{}})
	case "refresh_token":
		f.refreshes++
		presented := form.Get("refresh_token")
		if f.accessTokens[presented] {
			oauthRefusal(w, "invalid_grant", "The refresh token is invalid.")
			return
		}
		current, ok := f.chains[presented]
		if ok && current.used[presented] {
			current.revoked = true
		}
		if !ok || current.revoked || current.used[presented] {
			oauthRefusal(w, "invalid_grant", "The refresh token is invalid.")
			return
		}
		time.Sleep(20 * time.Millisecond) // widen the window two unlocked refreshes would race in
		current.used[presented] = true
		current.deadAccess = false
		f.mint(w, current)
	default:
		oauthRefusal(w, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token.")
	}
}

// revoke mirrors the server's revoke endpoint: 200 whatever the token, and the developer client's token loses its chain.
func (f *fakeMultiPaaS) revoke(w http.ResponseWriter, r *http.Request) {
	form, ok := f.form(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokes = append(f.revokes, form)
	if f.failRevoke {
		http.Error(w, "revoke failed", http.StatusInternalServerError)
		return
	}
	if current, ok := f.chains[form.Get("token")]; ok {
		current.revoked = true
	}
	w.WriteHeader(http.StatusOK)
}

// graphql mirrors the server's bearer path in its order: cookie, CLI version, bearer, scope. Every
// refusal is HTTP 200 with errors[0].extensions.code.
func (f *fakeMultiPaaS) graphql(w http.ResponseWriter, r *http.Request) {
	var request struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	scope := r.Header.Get("X-MultiPaaS-Scope")
	f.scopes = append(f.scopes, scope)
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	current, known := f.chains[token]
	switch {
	case r.Header.Get("Cookie") != "":
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
	case !supportedCLIVersion(r.Header.Get("X-MultiPaaS-Cli-Version")):
		graphQLRefusal(w, "UNSUPPORTED_CLI_VERSION", "upgrade the multipaas CLI")
	case !known || current.access != token || current.revoked || current.deadAccess:
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
	case scope != "" && !f.entitled(scope):
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
	default:
		f.answer(w, request.OperationName, scope, request.Variables)
	}
}

// answer serves the operations the CLI sends, shaped as the schema shapes them.
func (f *fakeMultiPaaS) answer(w http.ResponseWriter, operation, scope string, variables map[string]any) {
	parts := strings.Split(scope, "/")
	switch operation {
	case "Me":
		var name any = "Dev"
		if f.nameless {
			name = nil
		}
		writeData(w, map[string]any{"me": map[string]any{"id": "user-1", "email": "dev@example.com", "name": name}})
	case "MyTenants":
		options := []map[string]any{}
		for _, e := range f.entitlements {
			options = append(options, map[string]any{
				"client":        map[string]any{"id": e.clientID, "name": e.clientName},
				"applicationId": nullable(e.applicationID),
				"appTenantId":   nullable(e.appTenantID),
			})
		}
		writeData(w, map[string]any{"myTenants": options})
	case "Applications":
		if scope == "" {
			graphQLRefusal(w, "UNAUTHORIZED", "unauthorized") // requireTenant: no client selected
			return
		}
		writeData(w, map[string]any{"applications": page(f.apps[parts[0]], variables)})
	case "CreateAppTenant":
		f.createAppTenant(w, parts, variables)
	case "AppTenants":
		apps := []fakeRow{}
		if len(parts) >= 2 {
			apps = f.tenants[parts[1]]
		}
		writeData(w, map[string]any{"appTenants": page(apps, variables)})
	case "ScopedAppTenant":
		scopedApp := f.scopedApps[parts[len(parts)-1]]
		var tenant fakeRow
		for _, row := range f.tenants[scopedApp.id] {
			if row.id == parts[len(parts)-1] {
				tenant = row
			}
		}
		writeData(w, map[string]any{"me": map[string]any{
			"application": map[string]any{"id": scopedApp.id, "name": scopedApp.name},
			"appTenant":   map[string]any{"id": tenant.id, "name": tenant.name},
		}})
	case "Entities":
		writeData(w, map[string]any{"entities": pageOf(f.entities[appOf(parts)], variables)})
	case "EnumTypes":
		writeData(w, map[string]any{"enumTypes": pageOf(f.enumTypes[appOf(parts)], variables)})
	case "Fields":
		writeData(w, map[string]any{"fields": pageOf(f.fields[variables["entityId"].(string)], variables)})
	case "Constraints":
		writeData(w, map[string]any{"constraints": pageOf(f.constraints[variables["entityId"].(string)], variables)})
	case "LatestModelRevision":
		if f.failVersion {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		items := []map[string]any{}
		if n := f.revisions[appOf(parts)]; n > 0 {
			items = append(items, map[string]any{"id": fmt.Sprintf("rev-%d", n)})
		}
		writeData(w, map[string]any{"modelRevisions": map[string]any{"items": items}})
	case "CreateEntity", "UpdateEntity", "RetireEntity", "CreateEnumType", "UpdateEnumType", "RetireEnumType", "CreateField", "UpdateField", "RetireField", "CreateConstraint", "RetireConstraint":
		f.mutateModel(w, operation, parts, variables)
	case "ModelRevisions":
		if appOf(parts) == "" {
			graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
			return
		}
		writeData(w, map[string]any{"modelRevisions": pageOf(f.revisionRows(appOf(parts)), variables)})
	case "ApiSchema":
		var n int
		revisionID, _ := variables["revisionId"].(string)
		switch _, err := fmt.Sscanf(revisionID, "rev-%d", &n); {
		case appOf(parts) == "":
			graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		case err != nil || n < 1 || n > f.revisions[appOf(parts)]:
			graphQLRefusal(w, "NOT_FOUND", "modelRevision not found")
		default:
			writeData(w, map[string]any{"apiSchema": "type Query {\n  me: String\n}\n# " + revisionID + "\n"})
		}
	case "ApiSchemaDiff":
		from, _ := variables["fromRevisionId"].(string)
		to, _ := variables["toRevisionId"].(string)
		switch {
		case appOf(parts) == "":
			graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		case !f.hasRevision(appOf(parts), from) || !f.hasRevision(appOf(parts), to):
			graphQLRefusal(w, "NOT_FOUND", "modelRevision not found")
		default:
			change := func(kind string) []map[string]any {
				return []map[string]any{{"type": kind, "description": from + " to " + to}}
			}
			writeData(w, map[string]any{"apiSchemaDiff": map[string]any{"breaking": change("FIELD_REMOVED"), "dangerous": []map[string]any{}, "added": change("FIELD_ADDED")}})
		}
	case "Records":
		if len(parts) < 3 {
			graphQLRefusal(w, "UNAUTHORIZED", "unauthorized") // listRecords: no app-tenant selected
			return
		}
		rows := f.records[variables["entityId"].(string)]
		if reference, ok := variables["reference"].(map[string]any); ok {
			rows = slices.DeleteFunc(slices.Clone(rows), func(r map[string]any) bool {
				return r["data"].(map[string]any)[reference["fieldId"].(string)] != reference["recordId"]
			})
		}
		writeData(w, map[string]any{"records": pageOf(rows, variables)})
	case "Record":
		if len(parts) < 3 {
			graphQLRefusal(w, "UNAUTHORIZED", "unauthorized") // findRecord: no app-tenant selected
			return
		}
		i := slices.IndexFunc(f.records[variables["entityId"].(string)], func(r map[string]any) bool { return r["id"] == variables["id"] })
		if i < 0 {
			writeData(w, map[string]any{"record": nil})
			return
		}
		writeData(w, map[string]any{"record": f.records[variables["entityId"].(string)][i]})
	case "CreateRecord", "UpdateRecord", "RetireRecord":
		f.writeRecord(w, operation, parts, variables)
	case "OAuthClients", "CreateOAuthClient", "UpdateOAuthClient":
		f.manageOAuth(w, operation, parts, variables)
	default:
		http.Error(w, "unknown operation "+operation, http.StatusBadRequest)
	}
}

// entitled mirrors the server's scope check: a client-level entitlement covers any app and app-tenant
// under its client; an app-tenant-level one covers exactly its own application and app-tenant.
func (f *fakeMultiPaaS) entitled(scope string) bool {
	parts := strings.Split(scope, "/")
	if len(parts) > 3 || slices.Contains(parts, "") || f.refused[parts[0]] {
		return false
	}
	parts = append(parts, "", "")
	for _, e := range f.entitlements {
		if e.clientID == parts[0] && (e.appTenantID == "" || (e.appTenantID == parts[2] && e.applicationID == parts[1])) {
			return true
		}
	}
	return false
}

func (f *fakeMultiPaaS) mint(w http.ResponseWriter, current *chain) {
	current.access, current.refresh = randomToken(), randomToken()
	f.chains[current.access], f.chains[current.refresh] = current, current
	f.accessTokens[current.access] = true
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"access_token": current.access, "refresh_token": current.refresh, "token_type": "Bearer", "expires_in": f.accessTTL,
	})
}

func (f *fakeMultiPaaS) form(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		oauthRefusal(w, "invalid_request", "The body must be application/x-www-form-urlencoded.")
		return nil, false
	}
	if err := r.ParseForm(); err != nil || r.PostForm.Get("client_id") != developerClientID {
		writeJSONResponse(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client", "error_description": "Unknown client_id."})
		return nil, false
	}
	return r.PostForm, true
}

// The server's own shapes: a loopback callback URL and a 43-character base64url S256 challenge.
var (
	developerRedirect = regexp.MustCompile(`^http://(?:127\.0\.0\.1|localhost):([1-9]\d{0,4})/callback$`)
	codeChallenge     = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	cliVersion        = regexp.MustCompile(`^(\d{1,9})\.(\d{1,9})\.(\d{1,9})(?:[-+][0-9A-Za-z.+-]*)?$`)
)

// supportedCLIVersion accepts versions from 0.1.0 up, the oldest CLI the server accepts; a pre-release counts as its release.
func supportedCLIVersion(presented string) bool {
	match := cliVersion.FindStringSubmatch(presented)
	if match == nil {
		return false
	}
	have := [3]int{}
	for i := range have {
		have[i], _ = strconv.Atoi(match[i+1])
	}
	minimum := [3]int{0, 1, 0}
	for i := range have {
		if have[i] != minimum[i] {
			return have[i] > minimum[i]
		}
	}
	return true
}

func page(rows []fakeRow, variables map[string]any) map[string]any {
	input, _ := variables["page"].(map[string]any)
	number, _ := input["page"].(float64)
	size, _ := input["pageSize"].(float64)
	start, end := min(int(number*size), len(rows)), min(int((number+1)*size), len(rows))
	items := []map[string]any{}
	for _, row := range rows[start:end] {
		item := map[string]any{"id": row.id, "name": row.name}
		items = append(items, item)
	}
	return map[string]any{"items": items, "pageInfo": map[string]any{"hasMore": end < len(rows)}}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeData(w http.ResponseWriter, data any) {
	writeJSONResponse(w, http.StatusOK, map[string]any{"data": data})
}

func graphQLRefusal(w http.ResponseWriter, code, message string) {
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"data":   nil,
		"errors": []map[string]any{{"message": message, "extensions": map[string]any{"code": code}}},
	})
}

func oauthRefusal(w http.ResponseWriter, code, description string) {
	writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": code, "error_description": description})
}

func writeJSONResponse(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// mutateModel shares these steps with the server, in order: an application in scope (requireApplication),
// model.edit, then the change, which mints one revision. It does not check the input or that the entity
// exists, which the server does before model.edit. The changed row echoes the input with its id. A field input
// naming enumValues fails variable coercion, so it is answered HTTP 400 with no error code, before any of those steps.
func (f *fakeMultiPaaS) mutateModel(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	f.mutations = append(f.mutations, variables)
	if input, ok := variables["input"].(map[string]any); ok && strings.HasSuffix(operation, "Field") {
		if _, named := input["enumValues"]; named {
			writeJSONResponse(w, http.StatusBadRequest, map[string]any{"errors": []map[string]any{{
				"message": "Variable \"$input\" got invalid value; Field \"enumValues\" is not defined by type \"" + operation + "Input\".",
			}}})
			return
		}
	}
	switch {
	case appOf(parts) == "":
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	case f.noModelEdit:
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	case f.refuseChange[0] != "":
		graphQLRefusal(w, f.refuseChange[0], f.refuseChange[1])
		return
	}
	changed := map[string]any{"id": "new-1"}
	if input, ok := variables["input"].(map[string]any); ok {
		for key, value := range input {
			changed[key] = value
		}
	}
	for _, id := range []string{"entityId", "fieldId", "constraintId", "enumTypeId"} {
		if value, ok := variables[id]; ok {
			changed["id"] = value
		}
	}
	if input, ok := variables["input"].(map[string]any); ok && strings.HasPrefix(operation, "Update") {
		changed["id"] = input[map[string]string{"UpdateEntity": "entityId", "UpdateField": "fieldId", "UpdateEnumType": "enumTypeId"}[operation]]
	}
	f.revisions[appOf(parts)]++
	writeData(w, map[string]any{strings.ToLower(operation[:1]) + operation[1:]: changed})
}

// appOf is the application a scope names, or "" for a client-only or empty scope.
func appOf(parts []string) string {
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func pageOf(rows []map[string]any, variables map[string]any) map[string]any {
	input, _ := variables["page"].(map[string]any)
	number, _ := input["page"].(float64)
	size, _ := input["pageSize"].(float64)
	start, end := min(int(number*size), len(rows)), min(int((number+1)*size), len(rows))
	return map[string]any{"items": append([]map[string]any{}, rows[start:end]...), "pageInfo": map[string]any{"hasMore": end < len(rows)}}
}

// hasRevision is whether id is one of app's API versions, rev-1 to rev-N.
func (f *fakeMultiPaaS) hasRevision(app, id string) bool {
	var n int
	_, err := fmt.Sscanf(id, "rev-%d", &n)
	return err == nil && n >= 1 && n <= f.revisions[app]
}

// revisionRows are an app's revisions as modelRevisions lists them, newest first: rev-n down to rev-1.
func (f *fakeMultiPaaS) revisionRows(app string) []map[string]any {
	rows := []map[string]any{}
	for n := f.revisions[app]; n > 0; n-- {
		rows = append(rows, map[string]any{"id": fmt.Sprintf("rev-%d", n), "createdAt": "2026-10-11T00:00:00.000Z"})
	}
	return rows
}

// writeRecord shares these steps with the server, in order: an app-tenant in scope, then the write. An
// update merges data into the record after dropping clearFieldIds, as the server's does; it does not
// check the data against the entity's fields.
func (f *fakeMultiPaaS) writeRecord(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	f.mutations = append(f.mutations, variables)
	if len(parts) < 3 {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	input := variables["input"].(map[string]any)
	entity := input["entityId"].(string)
	field := strings.ToLower(operation[:1]) + operation[1:]
	if operation == "CreateRecord" {
		stamp := "2026-10-11T00:00:00.000Z"
		created := map[string]any{"id": fmt.Sprintf("record-%d", len(f.records[entity])+1), "data": input["data"], "createdAt": stamp, "updatedAt": stamp}
		f.records[entity] = append([]map[string]any{created}, f.records[entity]...)
		writeData(w, map[string]any{field: created})
		return
	}
	i := slices.IndexFunc(f.records[entity], func(r map[string]any) bool { return r["id"] == input["recordId"] })
	if i < 0 {
		graphQLRefusal(w, "NOT_FOUND", "record not found")
		return
	}
	record := maps.Clone(f.records[entity][i])
	if operation == "RetireRecord" {
		f.records[entity] = slices.Delete(f.records[entity], i, i+1)
		writeData(w, map[string]any{field: record})
		return
	}
	data := maps.Clone(record["data"].(map[string]any))
	cleared, _ := input["clearFieldIds"].([]any)
	for _, id := range cleared {
		delete(data, id.(string))
	}
	maps.Copy(data, input["data"].(map[string]any))
	record["data"] = data
	f.records[entity][i] = record
	writeData(w, map[string]any{field: record})
}

// createAppTenant shares these steps with the server, in order: an application in scope, then
// access.manage.application, then the insert.
func (f *fakeMultiPaaS) createAppTenant(w http.ResponseWriter, parts []string, variables map[string]any) {
	f.mutations = append(f.mutations, variables)
	app := appOf(parts)
	if app == "" || f.noManageApp {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	name, _ := variables["input"].(map[string]any)["name"].(string)
	created := fakeRow{id: fmt.Sprintf("tenant-new-%d", len(f.tenants[app])+1), name: name}
	f.tenants[app] = append(f.tenants[app], created)
	writeData(w, map[string]any{"createAppTenant": map[string]any{"id": created.id, "name": created.name}})
}

// manageOAuth shares these steps with the server, in order: an application in scope
// (requireApplication), application.manage_oauth (listing included), then the change. It does not check
// the redirect URIs, which the server does.
func (f *fakeMultiPaaS) manageOAuth(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	if operation != "OAuthClients" {
		f.mutations = append(f.mutations, variables)
	}
	if appOf(parts) == "" || f.noManageOAuth {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	input, _ := variables["input"].(map[string]any)
	switch operation {
	case "OAuthClients":
		writeData(w, map[string]any{"oauthClients": append([]map[string]any{}, f.oauthClients...)})
	case "CreateOAuthClient":
		created := map[string]any{"id": fmt.Sprintf("oauth-client-%d", len(f.oauthClients)+1), "name": input["name"], "redirectUris": input["redirectUris"], "createdAt": "2026-10-11T00:00:00.000Z"}
		f.oauthClients = append(f.oauthClients, created)
		writeData(w, map[string]any{"createOAuthClient": created})
	case "UpdateOAuthClient":
		i := slices.IndexFunc(f.oauthClients, func(c map[string]any) bool { return c["id"] == input["id"] })
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "oauthClient not found")
			return
		}
		updated := maps.Clone(f.oauthClients[i])
		updated["name"], updated["redirectUris"] = input["name"], input["redirectUris"]
		f.oauthClients[i] = updated
		writeData(w, map[string]any{"updateOAuthClient": updated})
	}
}
