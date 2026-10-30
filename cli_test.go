package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	signInTimeout = 10 * time.Second // a login the test never completes fails fast
	os.Exit(m.Run())
}

func TestLoginSavesDeveloperTokensForTheAPIURL(t *testing.T) {
	fake := signedOutFake(t)
	result := fake.run(t, "login", "--json")
	if result.exit != exitOK {
		t.Fatalf("exit %d, stdout %s, stderr %s", result.exit, result.stdout, result.stderr)
	}
	var who signedIn
	decode(t, result.stdout, &who)
	if strings.Contains(result.stdout, "stage") || who.User.Email != "dev@example.com" {
		t.Fatalf("login reported %+v", who)
	}
	info, err := os.Stat(credentialsPath(t))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file: %v, mode %v", err, info.Mode().Perm())
	}
	saved := readCredentials(t)[fake.server.URL]
	if saved.AccessToken == "" || saved.RefreshToken == "" || time.Until(saved.ExpiresAt) < 59*time.Minute {
		t.Fatalf("saved %+v", saved)
	}
}

func TestLoginPrintsTheURLWhenNoBrowserOpens(t *testing.T) {
	fake := signedOutFake(t)
	result := fake.runWith(t, func(url string) error { return errors.New("no browser") }, "login", "--json")
	if result.exit != exitOK || readCredentials(t)[fake.server.URL].AccessToken == "" {
		t.Fatalf("exit %d, stderr %s", result.exit, result.stderr)
	}
}

func TestLoginRefusedAtConsentIsNotSignedIn(t *testing.T) {
	fake := signedOutFake(t)
	fake.deny = true
	result := fake.run(t, "login", "--json")
	assertFailure(t, result, exitSignedOut, "SIGN_IN_REFUSED")
	if _, err := os.Stat(credentialsPath(t)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("credentials written after a refused sign-in: %v", err)
	}
}

func TestWhoamiWithoutSigningInExitsTwoWithoutCallingTheServer(t *testing.T) {
	fake := signedOutFake(t)
	assertFailure(t, fake.run(t, "whoami", "--json"), exitSignedOut, "NOT_SIGNED_IN")
	if len(fake.scopes) != 0 || fake.refreshes != 0 {
		t.Fatalf("server called: %d GraphQL, %d refresh", len(fake.scopes), fake.refreshes)
	}
}

func TestAnExpiredAccessTokenIsRefreshedAndTheRotatedPairSaved(t *testing.T) {
	fake := signedInFake(t, expired)
	before := readCredentials(t)[fake.server.URL]
	result := fake.run(t, "whoami", "--json")
	after := readCredentials(t)[fake.server.URL]
	if result.exit != exitOK || fake.refreshes != 1 || after.RefreshToken == before.RefreshToken {
		t.Fatalf("exit %d, %d refreshes, refresh token rotated: %v", result.exit, fake.refreshes, after.RefreshToken != before.RefreshToken)
	}
}

func TestATokenTheServerRefusesEarlyIsRefreshedOnceAndRetried(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.chains[readCredentials(t)[fake.server.URL].AccessToken].deadAccess = true
	result := fake.run(t, "whoami", "--json")
	if result.exit != exitOK || fake.refreshes != 1 {
		t.Fatalf("exit %d after %d refreshes: %s", result.exit, fake.refreshes, result.stdout)
	}
}

func TestARefusedRefreshSignsOut(t *testing.T) {
	fake := signedInFake(t, expired)
	fake.chains[readCredentials(t)[fake.server.URL].RefreshToken].revoked = true
	assertFailure(t, fake.run(t, "whoami", "--json"), exitSignedOut, "NOT_SIGNED_IN")
	if _, ok := readCredentials(t)[fake.server.URL]; ok {
		t.Fatal("a dead chain stayed in the credentials file")
	}
}

func TestConcurrentCommandsRefreshAnExpiredTokenOnlyOnce(t *testing.T) {
	fake := signedInFake(t, expired)
	results := make([]result, 4)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = fake.run(t, "whoami", "--json") })
	}
	wg.Wait()
	for _, got := range results {
		if got.exit != exitOK {
			t.Fatalf("a concurrent whoami exited %d: %s", got.exit, got.stdout)
		}
	}
	if fake.refreshes != 1 {
		t.Fatalf("%d refresh grants for one expired token", fake.refreshes)
	}
}

func TestAnOldOrUnstampedCLIIsToldToUpgrade(t *testing.T) {
	fake := signedInFake(t, fresh)
	outcomes := []string{}
	for _, version := range []string{"0.0.9", "dev", "0.1.0", "0.1.0-rc.1"} {
		got := fake.runAs(t, version, "whoami", "--json")
		outcomes = append(outcomes, fmt.Sprintf("%s:%d", version, got.exit))
		if got.exit == exitUpgrade && !strings.Contains(got.stdout, `"message": "upgrade the multipaas CLI"`) {
			t.Fatalf("exit 3 without the server's message: %s", got.stdout)
		}
	}
	if want := "0.0.9:3 dev:3 0.1.0:0 0.1.0-rc.1:0"; strings.Join(outcomes, " ") != want {
		t.Fatalf("got %v, want %s", outcomes, want)
	}
}

func TestAppsListCoversEveryEntitlementInItsOwnScope(t *testing.T) {
	fake := signedInFake(t, fresh)
	result := fake.run(t, "apps", "list", "--json")
	var apps []app
	decode(t, result.stdout, &apps)
	if result.exit != exitOK || len(apps) != 102 || strings.Contains(result.stdout, `"key"`) {
		t.Fatalf("exit %d, %d apps", result.exit, len(apps))
	}
	if last := apps[len(apps)-1]; last != (app{ClientID: "client-b", ClientName: "Beta", ID: "app-b", Name: "Orders"}) {
		t.Fatalf("app-tenant-level entitlement read as %+v", last)
	}
	if want := []string{"", "client-a", "client-a", "client-b/app-b/tenant-b"}; strings.Join(fake.scopes, ",") != strings.Join(want, ",") {
		t.Fatalf("scopes sent %q, want %q", fake.scopes, want)
	}
}

func TestTenantsListReadsTheAppsTenantsInTheAppsScope(t *testing.T) {
	fake := signedInFake(t, fresh)
	outcomes := []string{}
	for _, appID := range []string{"app-a-000", "app-b", "app-unknown"} {
		got := fake.run(t, "tenants", "list", "--app", appID, "--json")
		outcomes = append(outcomes, fmt.Sprintf("%s:%d:%s", appID, got.exit, compact(got.stdout)))
	}
	want := []string{
		`app-a-000:0:[{"id":"tenant-a","name":"Test"}]`,
		`app-b:0:[{"id":"tenant-b","name":"Beta test"}]`,
		`app-unknown:1:{"error":{"code":"NOT_FOUND","message":"no application app-unknown that you can reach"}}`,
	}
	if strings.Join(outcomes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(outcomes, "\n"), strings.Join(want, "\n"))
	}
}

func TestTenantsCreateMakesOneInTheAppsScopeAndListShowsIt(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "tenants", "create", "--app", "app-a-000", "--name", "Dev test", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if compact(got.stdout) != `{"appTenant":{"id":"tenant-new-2","name":"Dev test"}}` {
		t.Fatalf("stdout %s", got.stdout)
	}
	want := map[string]any{"input": map[string]any{"name": "Dev test"}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a/app-a-000" {
		t.Fatalf("scope %q", last)
	}
	listed := fake.run(t, "tenants", "list", "--app", "app-a-000", "--json")
	if compact(listed.stdout) != `[{"id":"tenant-a","name":"Test"},{"id":"tenant-new-2","name":"Dev test"}]` {
		t.Fatalf("list %s", listed.stdout)
	}
}

func TestTenantsCreateWithoutManageApplicationIsRefusedUnauthorized(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.noManageApp = true
	assertFailure(t, fake.run(t, "tenants", "create", "--app", "app-a-000", "--name", "Dev test", "--json"), exitRefused, "UNAUTHORIZED")
	if got := fake.run(t, "tenants", "list", "--app", "app-a-000", "--json"); compact(got.stdout) != `[{"id":"tenant-a","name":"Test"}]` {
		t.Fatalf("a refused create changed the list: %s", got.stdout)
	}
}

func TestTenantsCreateForAnApplicationYouCannotReachIsNotFoundAndSendsNothing(t *testing.T) {
	fake := signedInFake(t, fresh)
	assertFailure(t, fake.run(t, "tenants", "create", "--app", "app-unknown", "--name", "Dev test", "--json"), exitRefused, "NOT_FOUND")
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestAppsCreateMakesOneInYourOnlyClientsScopeAndListShowsIt(t *testing.T) {
	fake := signedInFake(t, fresh)
	got := fake.run(t, "apps", "create", "--name", "Orders", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if compact(got.stdout) != `{"application":{"clientId":"client-a","clientName":"Acme","id":"app-new-102","name":"Orders"}}` {
		t.Fatalf("stdout %s", got.stdout)
	}
	want := map[string]any{"input": map[string]any{"name": "Orders"}}
	if len(fake.mutations) != 1 || !reflect.DeepEqual(fake.mutations[0], want) {
		t.Fatalf("sent %v, want %v", fake.mutations, want)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-a" {
		t.Fatalf("scope %q", last)
	}
	var apps []app
	decode(t, fake.run(t, "apps", "list", "--json").stdout, &apps)
	if !slices.Contains(apps, app{ClientID: "client-a", ClientName: "Acme", ID: "app-new-102", Name: "Orders"}) {
		t.Fatalf("apps list does not show the new application: %d apps", len(apps))
	}
}

func TestAppsCreateWithMoreThanOneClientNeedsClientAndThenUsesIt(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.entitlements = append(fake.entitlements, entitlement{clientID: "client-c", clientName: "Gamma"})
	refused := fake.run(t, "apps", "create", "--name", "Orders", "--json")
	assertFailure(t, refused, exitRefused, "CLIENT_REQUIRED")
	if !strings.Contains(refused.stdout, "client-a (Acme)") || !strings.Contains(refused.stdout, "client-c (Gamma)") || strings.Contains(refused.stdout, "client-b") {
		t.Fatalf("the refusal should name exactly the client-level clients: %s", refused.stdout)
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
	got := fake.run(t, "apps", "create", "--client", "client-c", "--name", "Orders")
	if got.exit != exitOK || got.stdout != "application app-new-1 \"Orders\" in Gamma\n" {
		t.Fatalf("exit %d, stdout %q, stderr %s", got.exit, got.stdout, got.stderr)
	}
	if last := fake.scopes[len(fake.scopes)-1]; last != "client-c" {
		t.Fatalf("scope %q", last)
	}
}

func TestAppsCreateInAClientYouCannotCreateInIsNotFoundAndSendsNothing(t *testing.T) {
	fake := signedInFake(t, fresh)
	outcomes := []string{}
	for _, clientID := range []string{"client-b", "client-unknown"} {
		got := fake.run(t, "apps", "create", "--client", clientID, "--name", "Orders", "--json")
		outcomes = append(outcomes, fmt.Sprintf("%s:%d:%s", clientID, got.exit, compact(got.stdout)))
	}
	fake.entitlements = fake.entitlements[1:]
	got := fake.run(t, "apps", "create", "--name", "Orders", "--json")
	outcomes = append(outcomes, fmt.Sprintf("none:%d:%s", got.exit, compact(got.stdout)))
	want := []string{
		`client-b:1:{"error":{"code":"NOT_FOUND","message":"no client client-b you can create applications in"}}`,
		`client-unknown:1:{"error":{"code":"NOT_FOUND","message":"no client client-unknown you can create applications in"}}`,
		`none:1:{"error":{"code":"NOT_FOUND","message":"no client you can create applications in"}}`,
	}
	if strings.Join(outcomes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(outcomes, "\n"), strings.Join(want, "\n"))
	}
	if len(fake.mutations) != 0 {
		t.Fatalf("sent %v", fake.mutations)
	}
}

func TestAppsCreateWithoutCreatePermissionIsRefusedUnauthorized(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.noCreateApp = true
	assertFailure(t, fake.run(t, "apps", "create", "--name", "Orders", "--json"), exitRefused, "UNAUTHORIZED")
	var apps []app
	decode(t, fake.run(t, "apps", "list", "--json").stdout, &apps)
	if len(apps) != 102 {
		t.Fatalf("a refused create changed the list: %d apps", len(apps))
	}
}

func TestAScopeTheServerRefusesIsExitOneWithItsCode(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.entitlements = append(fake.entitlements, entitlement{clientID: "client-gone", clientName: "Gone"})
	fake.refused["client-gone"] = true
	assertFailure(t, fake.run(t, "apps", "list", "--json"), exitRefused, "UNAUTHORIZED")
	if fake.refreshes != 1 {
		t.Fatalf("%d refreshes; an UNAUTHORIZED is retried once after a refresh", fake.refreshes)
	}
}

func TestLogoutRevokesTheChainAndForgetsOnlyThatAPIURL(t *testing.T) {
	fake := signedInFake(t, fresh)
	writeCredentialsForTest(t, "https://api.other.example", credential{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)})
	refresh := readCredentials(t)[fake.server.URL].RefreshToken
	result := fake.run(t, "logout", "--json")
	if result.exit != exitOK || compact(result.stdout) != `{"profile":null,"signedOut":true}` {
		t.Fatalf("exit %d: %s", result.exit, result.stdout)
	}
	if len(fake.revokes) != 1 || fake.revokes[0].Get("token") != refresh || fake.revokes[0].Get("client_id") != developerClientID {
		t.Fatalf("revokes %v", fake.revokes)
	}
	if creds := readCredentials(t); len(creds) != 1 || creds["https://api.other.example"].AccessToken != "a" {
		t.Fatalf("credentials after logout: %+v", creds)
	}
	assertFailure(t, fake.run(t, "whoami", "--json"), exitSignedOut, "NOT_SIGNED_IN")
	if again := fake.run(t, "logout", "--json"); again.exit != exitOK || len(fake.revokes) != 1 {
		t.Fatalf("second logout: exit %d, %d revokes", again.exit, len(fake.revokes))
	}
}

func TestARequestTheServerNeverAnswersTimesOut(t *testing.T) {
	fake := signedInFake(t, expired)
	fake.hangToken = true
	defer func(was time.Duration) { requestTimeout = was }(requestTimeout)
	requestTimeout = 200 * time.Millisecond
	done := make(chan result, 1)
	go func() { done <- fake.run(t, "whoami", "--json") }()
	select {
	case got := <-done:
		assertFailure(t, got, exitRefused, "CLI_ERROR")
	case <-time.After(10 * time.Second):
		t.Fatal("whoami was still waiting for a server that never answers")
	}
	if _, ok := readCredentials(t)[fake.server.URL]; !ok {
		t.Fatal("a refresh that timed out removed the tokens")
	}
}

func TestAUserWithoutANameIsReportedWithANullName(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.nameless = true
	got := fake.run(t, "whoami", "--json")
	if got.exit != exitOK || compact(got.stdout) != `{"user":{"id":"user-1","email":"dev@example.com","name":null},"profile":null}` {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	fake.nameless = false
	if named := fake.run(t, "whoami", "--json"); compact(named.stdout) != `{"user":{"id":"user-1","email":"dev@example.com","name":"Dev"},"profile":null}` {
		t.Fatalf("a named user: %s", named.stdout)
	}
}

func TestARedirectIsNeverFollowed(t *testing.T) {
	fake := signedInFake(t, expired)
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { received.Add(1) }))
	defer target.Close()
	fake.redirectTo = target.URL
	assertFailure(t, fake.run(t, "whoami", "--json"), exitRefused, "CLI_ERROR")
	if received.Load() != 0 {
		t.Fatalf("the redirect target received %d requests", received.Load())
	}
	if _, ok := readCredentials(t)[fake.server.URL]; !ok {
		t.Fatal("a refused redirect removed the tokens")
	}
}

func TestACallbackWithAnotherStateIsRefused(t *testing.T) {
	fake := signedOutFake(t)
	foreign := make(chan int, 1)
	open := func(authorize string) error {
		go func() {
			parsed, _ := url.Parse(authorize)
			status := 0
			response, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=forged&state=not-this-sign-in")
			if err == nil {
				status = response.StatusCode
				_ = response.Body.Close()
			}
			foreign <- status
			_ = browse(authorize)
		}()
		return nil
	}
	got := fake.runWith(t, open, "login", "--json")
	if got.exit != exitOK || readCredentials(t)[fake.server.URL].AccessToken == "" {
		t.Fatalf("a forged callback broke the sign-in: exit %d: %s", got.exit, got.stdout)
	}
	if status := <-foreign; status != http.StatusNotFound {
		t.Fatalf("a callback with another state got HTTP %d, want 404", status)
	}
}

func TestTheSignedInPageIsDeliveredBeforeTheListenerStops(t *testing.T) {
	fake := signedOutFake(t)
	pages := make(chan string, 1)
	open := func(authorize string) error {
		go func() {
			body := ""
			response, err := http.Get(authorize)
			if err == nil {
				data, _ := io.ReadAll(response.Body)
				body = string(data)
				_ = response.Body.Close()
			}
			pages <- body
		}()
		return nil
	}
	got := fake.runWith(t, open, "login", "--json")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if page := <-pages; !strings.Contains(page, "Signed in to MultiPaaS. You can close this tab.") {
		t.Fatalf("the browser was left with %q", page)
	}
}

func TestLogoutForgetsTheTokensEvenWhenTheServerCannotRevokeThem(t *testing.T) {
	fake := signedInFake(t, fresh)
	writeCredentialsForTest(t, "https://api.other.example", credential{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)})
	fake.failRevoke = true
	got := fake.run(t, "logout", "--json")
	assertFailure(t, got, exitRefused, "REVOKE_FAILED")
	if !strings.Contains(got.stdout, "signed out on this machine") {
		t.Fatalf("the error does not say the tokens were forgotten locally: %s", got.stdout)
	}
	if creds := readCredentials(t); len(creds) != 1 || creds["https://api.other.example"].AccessToken != "a" {
		t.Fatalf("credentials after a failed revoke: %+v", creds)
	}
}

func TestAnAPIURLThatIsNotAnAPIHostIsRefusedBeforeAnyRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	exit := run([]string{"whoami", "--json"}, environment{
		stdout: &stdout, stderr: &stderr, version: "0.1.0", apiURL: "http://api.example.test", origins: originsFor, openBrowser: openBrowser,
	})
	assertFailure(t, result{exit: exit, stdout: stdout.String()}, exitRefused, "INVALID_API_URL")
}

func TestOriginsForDerivesWebappAPIFromTheAPIURL(t *testing.T) {
	outcomes := []string{}
	for _, apiURL := range []string{
		"", "https://api.example.test", "https://api.example.test/", "https://api.a.b.example.test",
		"http://api.example.test", "https://example.test", "https://webapp-api.example.test", "https://api.", "https://apix.example.test",
		"https://api.example.test/graphql", "https://api.example.test:8443", "https://api.example.test:", "https://api.example.test/#", "https://api.example.test#", "https://api..example.test", "https://api.example.test.", "https://api.example.test..", "https://api.example.test?x=1", "https://api.example.test?", "https://api.example.test#x",
		"https://user:pass@api.example.test", "api.example.test", "https://api.example.test:notaport", "%",
	} {
		hosts, err := originsFor(apiURL)
		outcomes = append(outcomes, fmt.Sprintf("%s=%s %s %v", apiURL, hosts.api, hosts.webappAPI, err != nil))
	}
	want := []string{
		"=https://api.multipaas.com https://webapp-api.multipaas.com false",
		"https://api.example.test=https://api.example.test https://webapp-api.example.test false",
		"https://api.example.test/=https://api.example.test https://webapp-api.example.test false",
		"https://api.a.b.example.test=https://api.a.b.example.test https://webapp-api.a.b.example.test false",
		"http://api.example.test=  true", "https://example.test=  true", "https://webapp-api.example.test=  true",
		"https://api.=  true", "https://apix.example.test=  true", "https://api.example.test/graphql=  true",
		"https://api.example.test:8443=  true", "https://api.example.test:=  true", "https://api.example.test/#=  true", "https://api.example.test#=  true",
		"https://api..example.test=  true", "https://api.example.test.=  true", "https://api.example.test..=  true",
		"https://api.example.test?x=1=  true", "https://api.example.test?=  true", "https://api.example.test#x=  true",
		"https://user:pass@api.example.test=  true", "api.example.test=  true", "https://api.example.test:notaport=  true", "%=  true",
	}
	if strings.Join(outcomes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(outcomes, "\n"))
	}
}

func TestCredentialsForOneAPIURLAreNotUsedForAnother(t *testing.T) {
	signedIn := signedInFake(t, fresh)
	other := newFakeMultiPaaS(t)
	assertFailure(t, other.run(t, "whoami", "--json"), exitSignedOut, "NOT_SIGNED_IN")
	if len(other.scopes) != 0 || other.refreshes != 0 || len(other.revokes) != 0 {
		t.Fatalf("the other URL was sent a token: %d GraphQL, %d refresh, %d revoke", len(other.scopes), other.refreshes, len(other.revokes))
	}
	if got := signedIn.run(t, "whoami", "--json"); got.exit != exitOK {
		t.Fatalf("the signed-in URL lost its sign-in: %d %s", got.exit, got.stdout)
	}
}

func TestAppsListPrintsATableWithoutJSON(t *testing.T) {
	fake := signedInFake(t, fresh)
	result := fake.run(t, "apps", "list")
	if result.exit != exitOK || !regexp.MustCompile(`(?m)^CLIENT\s+NAME\s+ID$`).MatchString(result.stdout) ||
		!regexp.MustCompile(`(?m)^Beta\s+Orders\s+app-b$`).MatchString(result.stdout) {
		t.Fatalf("exit %d:\n%s", result.exit, result.stdout)
	}
}

type result struct {
	exit           int
	stdout, stderr string
}

// run drives the real entry point against the fake, with a browser that follows every redirect.
func (f *fakeMultiPaaS) run(t *testing.T, args ...string) result {
	return f.runWith(t, browse, args...)
}

func (f *fakeMultiPaaS) runAs(t *testing.T, version string, args ...string) result {
	return f.drive(t, version, browse, "", args)
}

// runWithStdin is run with stdin as the command's standard input.
func (f *fakeMultiPaaS) runWithStdin(t *testing.T, stdin string, args ...string) result {
	return f.drive(t, "0.1.0", browse, stdin, args)
}

// runWith uses open as the browser; when it fails the test opens the URL login printed, as a person would.
func (f *fakeMultiPaaS) runWith(t *testing.T, open func(string) error, args ...string) result {
	return f.drive(t, "0.1.0", open, "", args)
}

func (f *fakeMultiPaaS) drive(t *testing.T, version string, open func(string) error, stdin string, args []string) result {
	t.Helper()
	var stdout bytes.Buffer
	stderr := &urlWatcher{}
	exit := run(args, environment{
		stdout: &stdout, stderr: stderr, stdin: strings.NewReader(stdin), version: version, origins: f.origins, profile: f.profile,
		openBrowser: func(url string) error {
			if err := open(url); err != nil {
				return browse(stderr.printedURL())
			}
			return nil
		},
	})
	return result{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

// browse is the person at the browser: it follows authorize to the loopback callback.
func browse(url string) error {
	go func() {
		response, err := http.Get(url)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	return nil
}

// urlWatcher is stderr, remembering the authorize URL login prints.
type urlWatcher struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *urlWatcher) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *urlWatcher) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *urlWatcher) printedURL() string {
	return regexp.MustCompile(`http://\S+/oauth/authorize\?\S+`).FindString(w.String())
}

func signedOutFake(t *testing.T) *fakeMultiPaaS {
	t.Setenv("HOME", t.TempDir())
	return newFakeMultiPaaS(t)
}

const (
	fresh   = false
	expired = true
)

// signedInFake is a fake with one client-level and one app-tenant-level entitlement, signed in to
// the fake, with the saved access token already past its expiry when expiredToken is set.
func signedInFake(t *testing.T, expiredToken bool) *fakeMultiPaaS {
	fake := signedOutFake(t)
	fake.entitlements = []entitlement{
		{clientID: "client-a", clientName: "Acme"},
		{clientID: "client-b", clientName: "Beta", applicationID: "app-b", appTenantID: "tenant-b"},
	}
	for i := range 101 {
		id := fmt.Sprintf("app-a-%03d", i)
		fake.apps["client-a"] = append(fake.apps["client-a"], fakeRow{id: id, name: "App " + id})
	}
	fake.tenants["app-a-000"] = []fakeRow{{id: "tenant-a", name: "Test"}}
	fake.tenants["app-b"] = []fakeRow{{id: "tenant-b", name: "Beta test"}}
	fake.scopedApps["tenant-b"] = fakeRow{id: "app-b", name: "Orders"}
	if got := fake.run(t, "login", "--json"); got.exit != exitOK {
		t.Fatalf("sign-in for the test failed: %d %s", got.exit, got.stdout)
	}
	if expiredToken {
		saved := readCredentials(t)[fake.server.URL]
		saved.ExpiresAt = time.Now().Add(-time.Second)
		writeCredentialsForTest(t, fake.server.URL, saved)
	}
	fake.scopes, fake.refreshes = nil, 0
	return fake
}

func assertFailure(t *testing.T, got result, exit int, code string) {
	t.Helper()
	var failure struct{ Error cliError }
	decode(t, got.stdout, &failure)
	if got.exit != exit || failure.Error.Code != code {
		t.Fatalf("exit %d code %q, want %d %q: %s", got.exit, failure.Error.Code, exit, code, got.stdout)
	}
}

func decode(t *testing.T, stdout string, into any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(stdout))
	if err := decoder.Decode(into); err != nil || decoder.More() {
		t.Fatalf("stdout is not exactly one JSON document (%v): %s", err, stdout)
	}
}

func compact(stdout string) string {
	var out bytes.Buffer
	_ = json.Compact(&out, []byte(stdout))
	return out.String()
}

func credentialsPath(t *testing.T) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "multipaas", "credentials.json")
}

func readCredentials(t *testing.T) credentialFile {
	t.Helper()
	creds := credentialFile{}
	data, err := os.ReadFile(credentialsPath(t))
	if err == nil {
		err = json.Unmarshal(data, &creds)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return creds
}

func writeCredentialsForTest(t *testing.T, apiURL string, saved credential) {
	t.Helper()
	if err := updateCredentials(func(creds credentialFile) error { creds[apiURL] = saved; return nil }); err != nil {
		t.Fatal(err)
	}
}
