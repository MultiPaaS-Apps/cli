package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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
	nextVersion  int                    // last version minted for a created record
	nextRecordID int                    // last id minted for a created record
	codes        map[string]pendingCode // code -> what it was issued for
	chains       map[string]*chain      // any token ever minted -> its chain
	refreshes    int                    // refresh grants answered
	revokes      []url.Values           // revoke forms received
	entitlements []entitlement          // what myTenants answers
	apps         map[string][]fakeRow   // client id -> its apps
	tenants      map[string][]fakeRow   // app id -> its app-tenants
	scopedApps   map[string]fakeRow     // app-tenant id -> its app
	environments map[string][]fakeEnv   // app id -> its environments, the default first
	envTenants   map[string][]fakeRow   // environment id -> its app-tenants
	envsMinted   map[string]int         // app id -> environment ids handed out, so a removal never frees one
	scopes       []string               // X-MultiPaaS-Scope per GraphQL request ("" when absent)
	refused      map[string]bool        // client ids myTenants lists but the scope check refuses
	nameless     bool                   // the signed-in user has no name
	hangToken    bool                   // the token endpoint never answers
	redirectTo   string                 // the token endpoint answers 307 to this URL
	failRevoke   bool                   // the revoke endpoint answers HTTP 500
	profile      string                 // MULTIPAAS_PROFILE for every command run against the fake
	onGraphQL    func(operation string) // runs on each GraphQL request, before it is answered
	release      chan struct{}          // closed at cleanup, to free handlers that never answer
	accessTokens map[string]bool        // every access token minted, so a refresh grant can refuse one

	entities                     map[string][]map[string]any // app id -> its entities, as model show reads them
	fields                       map[string][]map[string]any // entity id -> its fields
	enumTypes                    map[string][]map[string]any // app id -> its enum types
	constraints                  map[string][]map[string]any // entity id -> its constraints
	revisions                    map[string]int              // app id -> how many revisions its model has
	mutations                    []map[string]any            // the variables of every model mutation received
	noModelEdit                  bool                        // the signed-in user lacks model.edit
	refuseChange                 [2]string                   // a model mutation is refused with this code and message
	failVersion                  bool                        // LatestModelRevision answers HTTP 500
	records                      map[string][]map[string]any // entity id -> its live records, newest first; appWide marks the application's own
	recordScopes                 []string                    // the scope variable of every Records read ("" when absent)
	recordReads                  []recordRead                // every Records and RecordsCount request received
	refuseRecords                []map[string]any            // when set, every records read is refused as VALIDATION with these fieldErrors
	afterRecordsPage             func(f *fakeMultiPaaS)      // when set, runs after each Records page is answered
	omitNextCursor               bool                        // Records pages carry no nextCursor
	cursorCycle                  []string                    // when set, the nth Records page's nextCursor is cursorCycle[n mod len] and the sixth page ends the list
	lastQuery                    string                      // the query text of the GraphQL request being answered
	oauthClients                 []map[string]any            // the application's sign-in clients
	appIdentities                []map[string]any            // the application's app identities
	identitySeq                  int                         // app identities created so far; ids are never reused
	deployers                    []map[string]any            // the application's deployers
	deployerSeq                  int                         // deployers created so far; ids are never reused
	secretSeq                    int                         // app identity secrets created so far; ids are never reused
	noManageOAuth                bool                        // the signed-in user lacks application.manage_oauth
	scopedEnvNull                bool                        // an app-tenant-level read returns the app-tenant's environment as null
	removeNoData                 bool                        // removeEnvironment answers HTTP 200 with {"data":null} and no errors
	deployerID, deployerSecret   string                      // the deployer the fake knows; empty for none
	envClientID, envClientSecret string                      // MULTIPAAS_CLIENT_ID and _SECRET the CLI runs with
	deployerTokens               map[string]bool             // access tokens minted by the client-credentials grant
	deployerMints                int                         // client-credentials grants answered
	refuseFirstDeployerToken     bool                        // the first deployer token is refused, as an expired one is
	noManageApp                  bool                        // the signed-in user lacks access.manage.application
	noCreateApp                  bool                        // the signed-in user lacks client.applications.create
	roles                        []map[string]any            // the application's roles; appTenant is nil for an application role
	assignments                  []map[string]any            // role assignments: id, roleId, userId, appTenant
	roleSeq                      int                         // roles created so far; ids are never reused
	assignmentSeq                int                         // assignments created so far; ids are never reused

	imports          map[string]*fakeImport // import id -> its state
	uploads          map[string]fakeUpload  // File id -> what its PUT received
	importReads      int                    // DataImport reads answered
	refuseUpload     bool                   // the presigned PUT answers 403
	refuseImport     [2]string              // an import mutation is refused with this code and message
	refuseRead       [2]string              // a DataImport read is refused with this code and message
	refuseReadFields []map[string]any       // when set, a refused DataImport read is a VALIDATION refusal naming these fields
	invitationRows   []map[string]any       // the invitations ImportInvitations lists, filtered by the where it is sent
	invitationWheres []map[string]any       // the where of every ImportInvitations read
	reissued         map[string]any         // what ReissueImportInvitations answers: reissued and skipped
	failReport       bool                   // GET /report answers HTTP 500
	reportLines      []map[string]any       // GET /report serves these, one JSON line each
	statusQueue      map[string][]string    // import id -> the statuses its later DataImport reads answer
	fileCount        int                    // files registered so far, for part-N and file-N

	feedback   []map[string]any // the reports submitted, newest last
	cliVersion string           // X-MultiPaaS-Cli-Version of the latest GraphQL request
	autoSend   bool             // the signed-in user's feedback auto-send setting

	rawAnswers  []map[string]any // what the server answers each api command document, in order
	rawRequests []map[string]any // the query and variables of each api command document received
	rawStatus   int              // HTTP status of rawAnswers; 200 when zero
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

// env is the environment an app-tenant lives in; the empty string reads as the default environment.
type fakeRow struct{ id, name, env string }

type fakeEnv struct {
	id, name  string
	isDefault bool
	// protectionSet says a protect or unprotect has run; until then the default environment is protected and the rest are not.
	protectionSet, protected bool
}

func (e fakeEnv) deletionProtected() bool {
	if e.protectionSet {
		return e.protected
	}
	return e.isDefault
}

func newFakeMultiPaaS(t *testing.T) *fakeMultiPaaS {
	fake := &fakeMultiPaaS{
		t: t, accessTTL: 3600,
		codes: map[string]pendingCode{}, chains: map[string]*chain{},
		apps: map[string][]fakeRow{}, tenants: map[string][]fakeRow{}, scopedApps: map[string]fakeRow{}, environments: map[string][]fakeEnv{}, envsMinted: map[string]int{}, envTenants: map[string][]fakeRow{}, refused: map[string]bool{},
		release: make(chan struct{}), accessTokens: map[string]bool{}, deployerTokens: map[string]bool{},
		entities: map[string][]map[string]any{}, fields: map[string][]map[string]any{}, enumTypes: map[string][]map[string]any{}, constraints: map[string][]map[string]any{}, revisions: map[string]int{},
		records: map[string][]map[string]any{},
		imports: map[string]*fakeImport{}, uploads: map[string]fakeUpload{}, statusQueue: map[string][]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/authorize", fake.authorize)
	mux.HandleFunc("POST /oauth/token", fake.token)
	mux.HandleFunc("POST /oauth/revoke", fake.revoke)
	mux.HandleFunc("POST /graphql", fake.graphql)
	mux.HandleFunc("PUT /upload/{id}", fake.receiveUpload)
	mux.HandleFunc("GET /report", fake.serveReport)
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
	case "client_credentials":
		if form.Get("client_id") != f.deployerID || form.Get("client_secret") != f.deployerSecret {
			writeJSONResponse(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client", "error_description": "Unknown client, or a wrong or revoked secret."})
			return
		}
		f.deployerMints++
		current := &chain{used: map[string]bool{}, access: randomToken()}
		current.deadAccess = f.refuseFirstDeployerToken && f.deployerMints == 1
		f.chains[current.access], f.deployerTokens[current.access] = current, true
		writeJSONResponse(w, http.StatusOK, map[string]any{"access_token": current.access, "token_type": "Bearer", "expires_in": f.accessTTL})
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
		Query         string         `json:"query"`
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
	if f.onGraphQL != nil {
		f.onGraphQL(request.OperationName)
	}
	f.cliVersion = r.Header.Get("X-MultiPaaS-Cli-Version")
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if f.deployerTokens[token] {
		scope = f.deployerSlashScope(scope)
	}
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
	case request.OperationName == "" && len(f.rawAnswers) > 0:
		f.answerRaw(w, request.Query, request.Variables)
	default:
		f.lastQuery = request.Query
		f.answer(w, request.OperationName, scope, request.Variables)
	}
}

// deployerSlashScope is the slash form the fake answers a deployer's app: or tenant: scope in, so the rest of the fake serves it.
func (f *fakeMultiPaaS) deployerSlashScope(scope string) string {
	if tenant, ok := strings.CutPrefix(scope, "tenant:"); ok {
		for app, rows := range f.tenants {
			if slices.ContainsFunc(rows, func(r fakeRow) bool { return r.id == tenant }) {
				if slashed := f.deployerSlashScope("app:" + app); slashed != "app:"+app {
					return slashed + "/" + tenant
				}
			}
		}
		return scope
	}
	app, ok := strings.CutPrefix(scope, "app:")
	if !ok {
		return scope
	}
	for client, apps := range f.apps {
		if slices.ContainsFunc(apps, func(a fakeRow) bool { return a.id == app }) {
			return client + "/" + app
		}
	}
	return scope
}

// recordRead is one Records or RecordsCount request: its operation, query text and variables.
type recordRead struct {
	operation, query string
	variables        map[string]any
}

// answerRaw answers an api command document: each gets the next of rawAnswers, as the server would send it, and is
// kept in rawRequests. It runs after the token and scope checks, as a resolver would.
func (f *fakeMultiPaaS) answerRaw(w http.ResponseWriter, query string, variables map[string]any) {
	f.rawRequests = append(f.rawRequests, map[string]any{"query": query, "variables": variables})
	answer := f.rawAnswers[0]
	f.rawAnswers = f.rawAnswers[1:]
	status := f.rawStatus
	if status == 0 {
		status = http.StatusOK
	}
	writeJSONResponse(w, status, answer)
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
	case "CreateApplication":
		f.createApplication(w, parts, variables)
	case "CreateAppTenant":
		f.createAppTenant(w, parts, variables)
	case "AppTenants":
		apps := []fakeRow{}
		if inEnv, scoped := strings.CutPrefix(scope, "env:"); scoped {
			for app, envs := range f.environments {
				if slices.ContainsFunc(envs, func(e fakeEnv) bool { return e.id == inEnv }) {
					apps = f.tenantsInEnvironment(app, inEnv)
				}
			}
		} else if len(parts) >= 2 {
			apps = f.tenantsInEnvironment(parts[1], f.defaultEnvironment(parts[1]))
		}
		writeData(w, map[string]any{"appTenants": page(apps, variables)})
	case "Environments", "CreateEnvironment", "RenameEnvironment", "SetEnvironmentDeletionProtection", "RemoveEnvironment":
		f.manageEnvironments(w, operation, appOf(parts), variables)
	case "ScopedAppTenant":
		scopedApp := f.scopedApps[parts[len(parts)-1]]
		tenant, _ := f.tenantRow(scopedApp.id, parts[len(parts)-1])
		if f.scopedEnvNull {
			tenant.env = noEnvironment
		}
		writeData(w, map[string]any{"me": map[string]any{
			"application": map[string]any{"id": scopedApp.id, "name": scopedApp.name},
			"appTenant":   map[string]any{"id": tenant.id, "name": tenant.name, "environment": fakeEnvironment(cmp.Or(tenant.env, f.defaultEnvironment(scopedApp.id)))},
		}})
	case "Entities":
		writeData(w, map[string]any{"entities": pageOf(f.entities[appOf(parts)], variables)})
	case "EnumTypes":
		writeData(w, map[string]any{"enumTypes": pageOf(f.enumTypes[appOf(parts)], variables)})
	case "Field":
		writeData(w, map[string]any{"field": f.fieldByID(variables["id"].(string))})
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
	case "Records", "RecordsCount":
		scope, _ := variables["scope"].(string)
		f.recordScopes = append(f.recordScopes, scope)
		f.recordReads = append(f.recordReads, recordRead{operation: operation, query: f.lastQuery, variables: variables})
		if f.refuseRecords != nil {
			graphQLValidation(w, "where is invalid", f.refuseRecords)
			return
		}
		rows := slices.DeleteFunc(slices.Clone(f.records[variables["entityId"].(string)]), func(r map[string]any) bool {
			appWide := r["appWide"] == true
			return (scope == "APP_WIDE" && !appWide) || (scope == "TENANT" && appWide)
		})
		if reference, ok := variables["reference"].(map[string]any); ok {
			rows = slices.DeleteFunc(slices.Clone(rows), func(r map[string]any) bool {
				return r["data"].(map[string]any)[reference["fieldId"].(string)] != reference["recordId"]
			})
		}
		if operation == "RecordsCount" {
			writeData(w, map[string]any{"records": map[string]any{"totalCount": len(rows)}})
			return
		}
		for i, row := range rows {
			rows[i] = withFiles(row)
		}
		page := recordPage(rows, variables)
		if len(f.cursorCycle) > 0 {
			n := len(f.recordReads) - 1
			info := page["pageInfo"].(map[string]any)
			info["nextCursor"] = f.cursorCycle[n%len(f.cursorCycle)]
			info["hasMore"] = n < 5
		}
		if f.omitNextCursor {
			page["pageInfo"].(map[string]any)["nextCursor"] = nil
		}
		writeData(w, map[string]any{"records": page})
		if f.afterRecordsPage != nil {
			f.afterRecordsPage(f)
		}
	case "Record":
		i := slices.IndexFunc(f.records[variables["entityId"].(string)], func(r map[string]any) bool {
			return r["id"] == variables["id"] && recordInScope(r, parts, false)
		})
		if i < 0 {
			writeData(w, map[string]any{"record": nil})
			return
		}
		writeData(w, map[string]any{"record": withFiles(f.records[variables["entityId"].(string)][i])})
	case "CreateRecord", "UpdateRecord", "RetireRecord":
		f.writeRecord(w, operation, parts, variables)
	case "RequestRecordFileUpload":
		f.requestRecordFileUpload(w, variables)
	case "OAuthClients", "CreateOAuthClient", "UpdateOAuthClient":
		f.manageOAuth(w, operation, parts, variables)
	case "AppIdentities", "CreateAppIdentity", "CreateAppIdentitySecret", "RevokeAppIdentitySecret", "RetireAppIdentity":
		f.manageAppIdentity(w, operation, parts, variables)
	case "Deployers", "CreateDeployer", "RetireDeployer":
		f.manageDeployers(w, operation, variables)
	case "Roles", "PermissionCatalog", "CreateRole", "UpdateRole", "RoleAssignments", "AssignRole", "UnassignRole":
		f.manageRoles(w, operation, parts, variables)
	case "CreateDataImport", "AddDataImportFile", "RemoveDataImportFile", "CheckDataImport", "RunDataImport", "DeleteDataImport", "DataImport":
		f.dataImport(w, operation, parts, variables)
	case "ImportInvitations", "ReissueImportInvitations":
		f.importInvitations(w, operation, parts, variables)
	case "SubmitFeedback":
		f.submitFeedback(w, parts, variables)
	case "FeedbackSetting":
		writeData(w, map[string]any{"feedbackSetting": map[string]any{"autoSend": f.autoSend}})
	case "SetFeedbackSetting":
		f.mutations = append(f.mutations, variables)
		f.autoSend, _ = variables["autoSend"].(bool)
		writeData(w, map[string]any{"setFeedbackSetting": map[string]any{"autoSend": f.autoSend}})
	case "MyFeedback":
		newestFirst := slices.Clone(f.feedback)
		slices.Reverse(newestFirst)
		writeData(w, map[string]any{"myFeedback": pageOf(newestFirst, variables)})
	case "MyFeedbackReport":
		i := slices.IndexFunc(f.feedback, func(r map[string]any) bool { return r["id"] == variables["id"] })
		if i < 0 {
			writeData(w, map[string]any{"myFeedbackReport": nil})
			return
		}
		report := maps.Clone(f.feedback[i])
		report["history"] = []map[string]any{{"status": "received", "note": nil, "changedAt": "2026-10-11T00:00:00.000Z"}}
		writeData(w, map[string]any{"myFeedbackReport": report})
	default:
		http.Error(w, "unknown operation "+operation, http.StatusBadRequest)
	}
}

// entitled mirrors the server's scope check: a client-level entitlement covers any app and app-tenant
// under its client; an app-tenant-level one covers exactly its own application and app-tenant.
func (f *fakeMultiPaaS) entitled(scope string) bool {
	if env, ok := strings.CutPrefix(scope, "env:"); ok {
		return f.entitledToEnvironment(env)
	}
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

// knownClient is the developer client, or the deployer the fake knows.
func (f *fakeMultiPaaS) knownClient(id string) bool {
	return id == developerClientID || (f.deployerID != "" && id == f.deployerID)
}

func (f *fakeMultiPaaS) form(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		oauthRefusal(w, "invalid_request", "The body must be application/x-www-form-urlencoded.")
		return nil, false
	}
	if err := r.ParseForm(); err != nil || !f.knownClient(r.PostForm.Get("client_id")) {
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
		if row.env != "" {
			item["environment"] = fakeEnvironment(row.env)
		}
		items = append(items, item)
	}
	return map[string]any{"items": items, "pageInfo": map[string]any{"hasMore": end < len(rows)}}
}

// fakeEnvironment is an app-tenant's environment as the server prints it: null outside the caller's scope,
// which a row's env of noEnvironment stands for.
func fakeEnvironment(id string) any {
	if id == noEnvironment {
		return nil
	}
	return map[string]any{"id": id}
}

const noEnvironment = "none"

// manageDeployers answers the deployer operations; a retired deployer leaves the list.
func (f *fakeMultiPaaS) manageDeployers(w http.ResponseWriter, operation string, variables map[string]any) {
	switch operation {
	case "CreateDeployer":
		f.deployerSeq++
		created := map[string]any{"id": fmt.Sprintf("deployer-%d", f.deployerSeq), "name": variables["input"].(map[string]any)["name"], "lastFour": "abcd", "createdAt": "2026-11-08T00:00:00Z", "lastUsedAt": nil}
		f.deployers = append(f.deployers, created)
		writeData(w, map[string]any{"createDeployer": map[string]any{"deployer": created, "secret": "secret-abcd"}})
	case "RetireDeployer":
		i := slices.IndexFunc(f.deployers, func(d map[string]any) bool { return d["id"] == variables["id"] })
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "deployer not found")
			return
		}
		retired := f.deployers[i]
		f.deployers = slices.Delete(f.deployers, i, i+1)
		writeData(w, map[string]any{"retireDeployer": map[string]any{"id": retired["id"]}})
	default:
		writeData(w, map[string]any{"application": map[string]any{"deployers": f.deployers}})
	}
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

// graphQLValidation is a VALIDATION refusal naming the fields that failed, as the server's extensions carry them.
func graphQLValidation(w http.ResponseWriter, message string, fieldErrors []map[string]any) {
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"data":   nil,
		"errors": []map[string]any{{"message": message, "extensions": map[string]any{"code": "VALIDATION", "fieldErrors": fieldErrors}}},
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

// fieldByID is the field with id on any entity, or nil.
func (f *fakeMultiPaaS) fieldByID(id string) map[string]any {
	for _, fields := range f.fields {
		for _, field := range fields {
			if field["id"] == id {
				return field
			}
		}
	}
	return nil
}

func pageOf(rows []map[string]any, variables map[string]any) map[string]any {
	input, _ := variables["page"].(map[string]any)
	number, _ := input["page"].(float64)
	size, _ := input["pageSize"].(float64)
	start, end := min(int(number*size), len(rows)), min(int((number+1)*size), len(rows))
	return map[string]any{"items": append([]map[string]any{}, rows[start:end]...), "pageInfo": map[string]any{"hasMore": end < len(rows)}}
}

// recordPage is a page of records as the server pages them: after the cursor's record when one is given, else by
// page number, with a nextCursor naming the page's last record.
func recordPage(rows []map[string]any, variables map[string]any) map[string]any {
	input, _ := variables["page"].(map[string]any)
	number, _ := input["page"].(float64)
	size, _ := input["pageSize"].(float64)
	start := int(number * size)
	if cursor, ok := variables["cursor"].(string); ok {
		start = slices.IndexFunc(rows, func(r map[string]any) bool { return "after:"+r["id"].(string) == cursor }) + 1
	}
	start = min(start, len(rows))
	end := min(start+int(size), len(rows))
	items := append([]map[string]any{}, rows[start:end]...)
	var next any
	if len(items) > 0 {
		next = "after:" + items[len(items)-1]["id"].(string)
	}
	return map[string]any{"items": items, "pageInfo": map[string]any{"hasMore": end < len(rows), "nextCursor": next}}
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

// writeRecord shares these steps with the server: the write lands in the app-tenant in scope, or is app-wide when none is. An
// update merges data into the record after dropping clearFieldIds, as the server's does; it does not
// check the data against the entity's fields. An update with expectedVersion is refused with CONFLICT unless it matches the
// record's version, and every update changes the version.
func (f *fakeMultiPaaS) writeRecord(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	f.mutations = append(f.mutations, variables)
	input := variables["input"].(map[string]any)
	entity := input["entityId"].(string)
	field := strings.ToLower(operation[:1]) + operation[1:]
	if operation == "CreateRecord" {
		stamp := "2026-10-11T00:00:00.000Z"
		f.nextVersion++
		f.nextRecordID++
		created := map[string]any{"id": fmt.Sprintf("record-%d", f.nextRecordID), "appWide": len(parts) < 3, "data": input["data"], "createdAt": stamp, "updatedAt": stamp, "version": fmt.Sprintf("version-%d", f.nextVersion)}
		f.records[entity] = append([]map[string]any{created}, f.records[entity]...)
		writeData(w, map[string]any{field: withFiles(created)})
		return
	}
	i := slices.IndexFunc(f.records[entity], func(r map[string]any) bool {
		return r["id"] == input["recordId"] && recordInScope(r, parts, true)
	})
	if i < 0 {
		graphQLRefusal(w, "NOT_FOUND", "record not found")
		return
	}
	record := maps.Clone(f.records[entity][i])
	if operation == "RetireRecord" {
		f.records[entity] = slices.Delete(f.records[entity], i, i+1)
		writeData(w, map[string]any{field: withFiles(record)})
		return
	}
	if expected, ok := input["expectedVersion"].(string); ok && expected != record["version"] {
		graphQLRefusal(w, "CONFLICT", "record has changed since that version")
		return
	}
	record["version"] = fmt.Sprintf("%v+", record["version"])
	data := maps.Clone(record["data"].(map[string]any))
	cleared, _ := input["clearFieldIds"].([]any)
	for _, id := range cleared {
		delete(data, id.(string))
	}
	maps.Copy(data, input["data"].(map[string]any))
	record["data"] = data
	f.records[entity][i] = record
	writeData(w, map[string]any{field: withFiles(record)})
}

// recordInScope is the server's record policy: an app-wide request reaches only app-wide records, and a tenant
// request reads its tenant's records and the app-wide ones but writes only its tenant's.
func recordInScope(record map[string]any, parts []string, write bool) bool {
	if len(parts) < 3 {
		return record["appWide"] == true
	}
	return !write || record["appWide"] != true
}

// createApplication shares these steps with the server, in order: a client in scope (requireTenant),
// then client.applications.create, then the insert.
func (f *fakeMultiPaaS) createApplication(w http.ResponseWriter, parts []string, variables map[string]any) {
	f.mutations = append(f.mutations, variables)
	client := parts[0]
	if client == "" || f.noCreateApp {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	name, _ := variables["input"].(map[string]any)["name"].(string)
	created := fakeRow{id: fmt.Sprintf("app-new-%d", len(f.apps[client])+1), name: name}
	f.apps[client] = append(f.apps[client], created)
	writeData(w, map[string]any{"createApplication": map[string]any{"id": created.id, "name": created.name}})
}

// createAppTenant shares these steps with the server, in order: an application in scope, then
// access.manage.application, then the insert.
func (f *fakeMultiPaaS) createAppTenant(w http.ResponseWriter, parts []string, variables map[string]any) {
	f.mutations = append(f.mutations, variables)
	app := appOf(parts)
	env, inEnv := strings.CutPrefix(parts[0], "env:")
	if (app == "" && !inEnv) || f.noManageApp {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	name, _ := variables["input"].(map[string]any)["name"].(string)
	if inEnv {
		created := fakeRow{id: fmt.Sprintf("tenant-env-%d", f.envTenantCount()+1), name: name}
		f.envTenants[env] = append(f.envTenants[env], created)
		writeData(w, map[string]any{"createAppTenant": map[string]any{"id": created.id, "name": created.name}})
		return
	}
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

// scopeEnvironment is the environment a scope acts in: the named one, an app-tenant's own, else its
// application's default.
func (f *fakeMultiPaaS) scopeEnvironment(parts []string) string {
	if env, ok := strings.CutPrefix(parts[0], "env:"); ok {
		return env
	}
	if len(parts) > 2 {
		if row, found := f.tenantRow(parts[1], parts[2]); found {
			return cmp.Or(row.env, f.defaultEnvironment(parts[1]))
		}
	}
	return f.defaultEnvironment(appOf(parts))
}

// envTenantCount is the app-tenants held in f.envTenants across every environment, so a new id never repeats.
func (f *fakeMultiPaaS) envTenantCount() int {
	count := 0
	for _, held := range f.envTenants {
		count += len(held)
	}
	return count
}

// tenantRows is every app-tenant of app with the environment it lives in: those in f.tenants, where an empty
// env is the default, and those in f.envTenants under an environment that app lists.
func (f *fakeMultiPaaS) tenantRows(app string) []fakeRow {
	rows := slices.Clone(f.tenants[app])
	for i := range rows {
		rows[i].env = cmp.Or(rows[i].env, f.defaultEnvironment(app))
	}
	for _, e := range f.environments[app] {
		for _, row := range f.envTenants[e.id] {
			row.env = e.id
			rows = append(rows, row)
		}
	}
	return rows
}

// tenantRow is the app-tenant id of app, with its environment.
func (f *fakeMultiPaaS) tenantRow(app, id string) (fakeRow, bool) {
	rows := f.tenantRows(app)
	at := slices.IndexFunc(rows, func(row fakeRow) bool { return row.id == id })
	if at < 0 {
		return fakeRow{}, false
	}
	return rows[at], true
}

// tenantsInEnvironment is the app-tenants of app that live in env.
func (f *fakeMultiPaaS) tenantsInEnvironment(app, env string) []fakeRow {
	return slices.DeleteFunc(f.tenantRows(app), func(row fakeRow) bool { return row.env != env })
}

// defaultEnvironment is the application's default environment, env-default when it has none seeded.
func (f *fakeMultiPaaS) defaultEnvironment(app string) string {
	for _, e := range f.environments[app] {
		if e.isDefault {
			return e.id
		}
	}
	return "env-default"
}

// manageAppIdentity takes the same steps as manageOAuth: an application in scope, then
// application.manage_oauth, then the change. A secret's plaintext is in the create answers only.
func (f *fakeMultiPaaS) manageAppIdentity(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	if operation != "AppIdentities" {
		f.mutations = append(f.mutations, variables)
	}
	if (appOf(parts) == "" && !strings.HasPrefix(parts[0], "env:")) || f.noManageOAuth {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	scopeEnv := f.scopeEnvironment(parts)
	inScope := func(identity map[string]any) bool {
		environment, placed := identity["environment"].(map[string]any)
		return !placed || environment["id"] == scopeEnv
	}
	secretsOf := func(identity map[string]any) []map[string]any { return identity["secrets"].([]map[string]any) }
	addSecret := func(i int) string {
		f.secretSeq++
		n := f.secretSeq
		identity := maps.Clone(f.appIdentities[i])
		identity["secrets"] = append(slices.Clone(secretsOf(identity)), map[string]any{
			"id": fmt.Sprintf("secret-%d", n), "lastFour": fmt.Sprintf("%04d", n), "createdAt": "2026-10-27T00:00:00.000Z", "lastUsedAt": nil})
		f.appIdentities[i] = identity
		return fmt.Sprintf("mps_plaintext_%d", n)
	}
	byID := func(id any) int {
		return slices.IndexFunc(f.appIdentities, func(a map[string]any) bool { return a["id"] == id && inScope(a) })
	}
	switch operation {
	case "AppIdentities":
		listed := []map[string]any{}
		for _, identity := range f.appIdentities {
			if inScope(identity) {
				listed = append(listed, identity)
			}
		}
		writeData(w, map[string]any{"appIdentities": listed})
	case "CreateAppIdentity":
		input, _ := variables["input"].(map[string]any)
		f.identitySeq++
		n := f.identitySeq
		f.appIdentities = append(f.appIdentities, map[string]any{"id": fmt.Sprintf("app-identity-%d", n), "name": input["name"], "environment": map[string]any{"id": scopeEnv},
			"user": map[string]any{"id": fmt.Sprintf("service-user-%d", n)}, "createdAt": "2026-10-27T00:00:00.000Z", "secrets": []map[string]any{}})
		last := len(f.appIdentities) - 1
		secret := addSecret(last)
		writeData(w, map[string]any{"createAppIdentity": map[string]any{"appIdentity": f.appIdentities[last], "secret": secret}})
	case "CreateAppIdentitySecret":
		i := byID(variables["id"])
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "appIdentity not found")
			return
		}
		secret := addSecret(i)
		writeData(w, map[string]any{"createAppIdentitySecret": map[string]any{"appIdentity": f.appIdentities[i], "secret": secret}})
	case "RevokeAppIdentitySecret":
		for i, identity := range f.appIdentities {
			if !inScope(identity) {
				continue
			}
			if j := slices.IndexFunc(secretsOf(identity), func(s map[string]any) bool { return s["id"] == variables["id"] }); j >= 0 {
				revoked := maps.Clone(identity)
				revoked["secrets"] = slices.Delete(slices.Clone(secretsOf(identity)), j, j+1)
				f.appIdentities[i] = revoked
				writeData(w, map[string]any{"revokeAppIdentitySecret": revoked})
				return
			}
		}
		graphQLRefusal(w, "NOT_FOUND", "secret not found")
	case "RetireAppIdentity":
		i := byID(variables["id"])
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "appIdentity not found")
			return
		}
		retired := f.appIdentities[i]
		f.appIdentities = slices.Delete(f.appIdentities, i, i+1)
		writeData(w, map[string]any{"retireAppIdentity": retired})
	}
}

// manageRoles serves roles as the server scopes them. A role or assignment made in an app-tenant's scope
// belongs to that app-tenant, and one made in the application's scope belongs to the application. A read in an
// app-tenant's scope sees both, so the CLI must do its own filtering by scope.
func (f *fakeMultiPaaS) manageRoles(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	write := operation == "CreateRole" || operation == "UpdateRole" || operation == "AssignRole" || operation == "UnassignRole"
	if write {
		f.mutations = append(f.mutations, variables)
	}
	if (appOf(parts) == "" && !strings.HasPrefix(parts[0], "env:")) || (write && f.noManageApp) {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	var tenant any
	if len(parts) > 2 {
		tenant = map[string]any{"id": parts[2]}
	}
	sameScope := func(row map[string]any) bool { return reflect.DeepEqual(row["appTenant"], tenant) }
	visible := func(row map[string]any) bool { return row["appTenant"] == nil || sameScope(row) }
	roleByID := func(id any) map[string]any {
		if i := slices.IndexFunc(f.roles, func(r map[string]any) bool { return r["id"] == id }); i >= 0 {
			return f.roles[i]
		}
		return nil
	}
	shown := func(a map[string]any) map[string]any {
		return map[string]any{"id": a["id"], "appTenant": a["appTenant"], "role": roleByID(a["roleId"])}
	}
	switch operation {
	case "Roles":
		listed := []map[string]any{}
		for _, role := range f.roles {
			if visible(role) {
				listed = append(listed, role)
			}
		}
		writeData(w, map[string]any{"roles": listed})
	case "PermissionCatalog":
		writeData(w, map[string]any{"permissionCatalog": []map[string]any{
			{"key": "record.view_all", "level": "appTenant", "entityTargeted": true},
			{"key": "record.app_wide.create", "level": "application", "entityTargeted": true},
		}})
	case "CreateRole":
		input, _ := variables["input"].(map[string]any)
		if slices.ContainsFunc(f.roles, func(r map[string]any) bool { return sameScope(r) && r["key"] == input["key"] }) {
			graphQLRefusal(w, "DUPLICATE_ROLE_KEY", "a role with this key already exists")
			return
		}
		permissions := rolePermissionRows(input["permissions"])
		f.roleSeq++
		role := map[string]any{"id": fmt.Sprintf("role-%d", f.roleSeq), "key": input["key"], "name": input["name"], "appTenant": tenant, "permissions": permissions}
		f.roles = append(f.roles, role)
		writeData(w, map[string]any{"createRole": role})
	case "UpdateRole":
		input, _ := variables["input"].(map[string]any)
		role := roleByID(input["id"])
		if role == nil || !sameScope(role) {
			graphQLRefusal(w, "NOT_FOUND", "role not found")
			return
		}
		if name, ok := input["name"].(string); ok {
			role["name"] = name
		}
		if input["permissions"] != nil {
			role["permissions"] = rolePermissionRows(input["permissions"])
		}
		writeData(w, map[string]any{"updateRole": role})
	case "RoleAssignments":
		listed := []map[string]any{}
		for _, a := range f.assignments {
			if visible(a) && a["userId"] == variables["userId"] {
				listed = append(listed, shown(a))
			}
		}
		writeData(w, map[string]any{"roleAssignments": listed})
	case "AssignRole":
		role := roleByID(variables["roleId"])
		if role == nil || !visible(role) {
			graphQLRefusal(w, "NOT_FOUND", "role not found")
			return
		}
		if !slices.ContainsFunc(f.appIdentities, func(a map[string]any) bool { return a["user"].(map[string]any)["id"] == variables["userId"] }) {
			graphQLRefusal(w, "NOT_FOUND", "user not found")
			return
		}
		if slices.ContainsFunc(f.assignments, func(a map[string]any) bool {
			return sameScope(a) && a["roleId"] == variables["roleId"] && a["userId"] == variables["userId"]
		}) {
			graphQLRefusal(w, "DUPLICATE_ROLE_ASSIGNMENT", "this user already holds this role")
			return
		}
		f.assignmentSeq++
		assignment := map[string]any{"id": fmt.Sprintf("assignment-%d", f.assignmentSeq), "roleId": variables["roleId"], "userId": variables["userId"], "appTenant": tenant}
		f.assignments = append(f.assignments, assignment)
		writeData(w, map[string]any{"assignRole": shown(assignment)})
	case "UnassignRole":
		i := slices.IndexFunc(f.assignments, func(a map[string]any) bool { return a["id"] == variables["id"] && sameScope(a) })
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "roleAssignment not found")
			return
		}
		removed := shown(f.assignments[i])
		f.assignments = slices.Delete(f.assignments, i, i+1)
		writeData(w, map[string]any{"unassignRole": removed})
	}
}

// rolePermissionRows is a role's permissions as the server returns them, from the permissions an input sent.
func rolePermissionRows(sent any) []map[string]any {
	rows := []map[string]any{}
	given, _ := sent.([]any)
	for _, item := range given {
		p := item.(map[string]any)
		rows = append(rows, map[string]any{"permission": p["permission"], "entityId": p["entityId"]})
	}
	return rows
}

// submitFeedback files a report as the server does: details "too-long" is refused as VALIDATION and "throttle"
// as RATE_LIMITED; anything else is stored. The summary is the trimmed one sent, else null. The application and
// client are named only when the scope has an application and the input does not say aboutApplication is false.
func (f *fakeMultiPaaS) submitFeedback(w http.ResponseWriter, parts []string, variables map[string]any) {
	f.mutations = append(f.mutations, variables)
	input, _ := variables["input"].(map[string]any)
	details, _ := input["details"].(string)
	switch details {
	case "too-long":
		graphQLValidation(w, "details is invalid", []map[string]any{{"field": "details", "rule": "maxLength", "message": "Details must be at most 10000 characters"}})
		return
	case "throttle":
		graphQLRefusal(w, "RATE_LIMITED", "You've sent a lot of feedback in the last hour. Try again later.")
		return
	}
	var summary any
	if sent, _ := input["summary"].(string); strings.TrimSpace(sent) != "" {
		summary = strings.TrimSpace(sent)
	}
	source := "cli"
	if input["viaPlugin"] == true {
		source = "plugin"
	}
	var application, client any
	if input["aboutApplication"] != false && appOf(parts) != "" {
		application = map[string]any{"id": appOf(parts), "name": f.appName(appOf(parts))}
		client = map[string]any{"id": parts[0], "name": f.clientName(parts[0])}
	}
	report := map[string]any{
		"id": fmt.Sprintf("feedback-%d", len(f.feedback)+1), "kind": input["kind"], "status": "received",
		"summary": summary, "details": details, "source": source, "cliVersion": f.cliVersion, "apiVersion": input["apiVersion"],
		"platformVersion": "1.0.0", "application": application, "client": client, "sentFromRemovedScope": false,
		"createdAt": "2026-10-11T00:00:00.000Z", "updatedAt": "2026-10-11T00:00:00.000Z",
	}
	f.feedback = append(f.feedback, report)
	writeData(w, map[string]any{"submitFeedback": report})
}

// clientName is the name of client id, as apps list shows it.
func (f *fakeMultiPaaS) clientName(id string) string {
	for _, e := range f.entitlements {
		if e.clientID == id {
			return e.clientName
		}
	}
	return ""
}

// appName is the name of application id, as apps list shows it.
func (f *fakeMultiPaaS) appName(id string) string {
	for _, rows := range f.apps {
		for _, row := range rows {
			if row.id == id {
				return row.name
			}
		}
	}
	for _, row := range f.scopedApps {
		if row.id == id {
			return row.name
		}
	}
	return ""
}

// entitledToEnvironment mirrors the server resolving an env: scope to its application: a client-level
// entitlement covers every environment of an application under its client.
func (f *fakeMultiPaaS) entitledToEnvironment(env string) bool {
	for app, envs := range f.environments {
		if !slices.ContainsFunc(envs, func(e fakeEnv) bool { return e.id == env }) {
			continue
		}
		for client, apps := range f.apps {
			if slices.ContainsFunc(apps, func(a fakeRow) bool { return a.id == app }) && !f.refused[client] {
				return slices.ContainsFunc(f.entitlements, func(e entitlement) bool { return e.clientID == client && e.appTenantID == "" })
			}
		}
	}
	return false
}

// removalRefusal is the server's reason and message for refusing a removal, default first, or "" when it is allowed.
func removalRefusal(e fakeEnv) (reason, message string) {
	if e.isDefault {
		return "environment_default", "The default environment can't be removed."
	}
	if e.deletionProtected() {
		return "environment_protected", "This environment has deletion protection on. Turn it off before removing the environment."
	}
	return "", ""
}

// manageEnvironments shares these steps with the server, in order: an application in scope, then
// access.manage.application for a change, then the change. Names are unique within an application.
func (f *fakeMultiPaaS) manageEnvironments(w http.ResponseWriter, operation, app string, variables map[string]any) {
	if operation != "Environments" {
		f.mutations = append(f.mutations, variables)
	}
	if app == "" || (operation != "Environments" && f.noManageApp) {
		graphQLRefusal(w, "UNAUTHORIZED", "unauthorized")
		return
	}
	shown := func(e fakeEnv) map[string]any {
		return map[string]any{"id": e.id, "name": e.name, "isDefault": e.isDefault, "deletionProtected": e.deletionProtected()}
	}
	input, _ := variables["input"].(map[string]any)
	name, _ := input["name"].(string)
	if operation != "Environments" && slices.ContainsFunc(f.environments[app], func(e fakeEnv) bool { return e.name == name }) {
		graphQLRefusal(w, "VALIDATION", "an environment with this name already exists")
		return
	}
	switch operation {
	case "CreateEnvironment":
		f.envsMinted[app] = max(f.envsMinted[app], len(f.environments[app])) + 1
		created := fakeEnv{id: fmt.Sprintf("env-new-%d", f.envsMinted[app]), name: name}
		f.environments[app] = append(f.environments[app], created)
		writeData(w, map[string]any{"createEnvironment": shown(created)})
	case "RenameEnvironment":
		i := slices.IndexFunc(f.environments[app], func(e fakeEnv) bool { return e.id == input["id"] })
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "environment not found")
			return
		}
		f.environments[app][i].name = name
		writeData(w, map[string]any{"renameEnvironment": shown(f.environments[app][i])})
	case "SetEnvironmentDeletionProtection":
		i := slices.IndexFunc(f.environments[app], func(e fakeEnv) bool { return e.id == input["id"] })
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "environment not found")
			return
		}
		f.environments[app][i].protectionSet = true
		f.environments[app][i].protected, _ = input["protected"].(bool)
		writeData(w, map[string]any{"setEnvironmentDeletionProtection": shown(f.environments[app][i])})
	case "RemoveEnvironment":
		if f.removeNoData {
			writeData(w, nil)
			return
		}
		i := slices.IndexFunc(f.environments[app], func(e fakeEnv) bool { return e.id == input["id"] })
		if i < 0 {
			graphQLRefusal(w, "NOT_FOUND", "environment not found")
			return
		}
		if reason, message := removalRefusal(f.environments[app][i]); reason != "" {
			writeJSONResponse(w, http.StatusOK, map[string]any{
				"data":   nil,
				"errors": []map[string]any{{"message": message, "extensions": map[string]any{"code": "FORBIDDEN", "reason": reason}}},
			})
			return
		}
		removed := f.environments[app][i]
		f.environments[app] = slices.Delete(f.environments[app], i, i+1)
		writeData(w, map[string]any{"removeEnvironment": shown(removed)})
	default:
		listed := []map[string]any{}
		for _, e := range f.environments[app] {
			listed = append(listed, shown(e))
		}
		writeData(w, map[string]any{"me": map[string]any{"application": map[string]any{"environments": listed}}})
	}
}
