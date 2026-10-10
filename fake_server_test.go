package main

import (
	"encoding/json"
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

// answer serves the five operations the CLI sends, shaped as the schema shapes them.
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
