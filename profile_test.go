package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTwoProfilesForOneAPIURLAreKeptSideBySide(t *testing.T) {
	fake := signedInFake(t, fresh)
	unnamed := readCredentials(t)[fake.server.URL]
	got := fake.run(t, "login", "--profile", "acme", "--json")
	creds := readCredentials(t)
	acme, ok := creds[fake.server.URL+"#acme"]
	if len(creds) != 2 || !ok || creds[fake.server.URL] != unnamed || acme.RefreshToken == unnamed.RefreshToken {
		t.Fatalf("credentials after a second sign-in: %+v", creds)
	}
	if got.exit != exitOK {
		t.Fatalf("login --profile acme: exit %d: %s", got.exit, got.stdout)
	}
}

func TestLogoutOfAProfileLeavesTheUnnamedSignInIntact(t *testing.T) {
	fake := signedInFake(t, fresh)
	unnamed := readCredentials(t)[fake.server.URL]
	if got := fake.run(t, "login", "--profile", "acme", "--json"); got.exit != exitOK {
		t.Fatalf("login --profile acme: exit %d: %s", got.exit, got.stdout)
	}
	if got := fake.run(t, "logout", "--profile", "acme", "--json"); got.exit != exitOK || compact(got.stdout) != `{"profile":"acme","signedOut":true}` {
		t.Fatalf("logout --profile acme: exit %d: %s", got.exit, got.stdout)
	}
	if creds := readCredentials(t); len(creds) != 1 || creds[fake.server.URL] != unnamed {
		t.Fatalf("logout of a profile touched another sign-in: %+v", creds)
	}
	if got := fake.run(t, "whoami", "--json"); got.exit != exitOK {
		t.Fatalf("the unnamed sign-in stopped working: exit %d: %s", got.exit, got.stdout)
	}
}

func TestMULTIPAASPROFILEChoosesTheSignIn(t *testing.T) {
	fake := signedOutFake(t)
	fake.profile = "acme"
	if got := fake.run(t, "login", "--json"); got.exit != exitOK || compact(got.stdout) != `{"user":{"id":"user-1","email":"dev@example.com","name":"Dev"},"profile":"acme"}` {
		t.Fatalf("login with MULTIPAAS_PROFILE=acme: exit %d: %s", got.exit, got.stdout)
	}
	if _, ok := readCredentials(t)[fake.server.URL+"#acme"]; !ok {
		t.Fatalf("not saved under the profile's key: %+v", readCredentials(t))
	}
	fake.profile = ""
	assertFailure(t, fake.run(t, "whoami", "--json"), exitSignedOut, "NOT_SIGNED_IN")
}

func TestTheProfileFlagOverridesMULTIPAASPROFILE(t *testing.T) {
	fake := signedOutFake(t)
	fake.profile = "acme"
	if got := fake.run(t, "login", "--json"); got.exit != exitOK {
		t.Fatalf("login with MULTIPAAS_PROFILE=acme: exit %d: %s", got.exit, got.stdout)
	}
	other := fake.run(t, "whoami", "--profile", "globex", "--json")
	assertFailure(t, other, exitSignedOut, "NOT_SIGNED_IN")
	if !strings.Contains(other.stdout, `not signed in with profile \"globex\"; run: multipaas login --profile globex`) {
		t.Fatalf("NOT_SIGNED_IN does not say how to sign in to the profile: %s", other.stdout)
	}
}

func TestAnEmptyProfileFlagFallsBackToMULTIPAASPROFILE(t *testing.T) {
	fake := signedOutFake(t)
	fake.profile = "acme"
	if got := fake.run(t, "login", "--json"); got.exit != exitOK {
		t.Fatalf("login with MULTIPAAS_PROFILE=acme: exit %d: %s", got.exit, got.stdout)
	}
	if got := fake.run(t, "whoami", "--profile", "", "--json"); got.exit != exitOK {
		t.Fatalf("an empty --profile did not fall back to MULTIPAAS_PROFILE: exit %d: %s", got.exit, got.stdout)
	}
}

func TestAProfileNameThatIsNotAllowedIsRefused(t *testing.T) {
	fake := signedOutFake(t)
	for _, name := range []string{"Acme", "-acme", "ac#me", "a/b", strings.Repeat("a", 33)} {
		got := fake.run(t, "login", "--profile", name, "--json")
		assertFailure(t, got, exitRefused, "INVALID_PROFILE")
		if !strings.Contains(got.stdout, `must start with a lowercase letter or digit, then lowercase letters, digits, `) {
			t.Fatalf("--profile %q: %s", name, got.stdout)
		}
	}
}

func TestAProfileNameFromTheEnvironmentThatIsNotAllowedIsRefused(t *testing.T) {
	fake := signedOutFake(t)
	fake.profile = "Acme"
	assertFailure(t, fake.run(t, "whoami", "--json"), exitRefused, "INVALID_PROFILE")
}

func TestARefusedProfileNameReachesNeitherTheServerNorTheStore(t *testing.T) {
	fake := signedOutFake(t)
	assertFailure(t, fake.run(t, "login", "--profile", "Acme", "--json"), exitRefused, "INVALID_PROFILE")
	if len(fake.codes) != 0 || len(fake.scopes) != 0 || len(readCredentials(t)) != 0 {
		t.Fatalf("a refused profile name reached the server or the store: %d codes, %d GraphQL", len(fake.codes), len(fake.scopes))
	}
}

func TestAProfileNameOf32CharactersWithUnderscoreAndHyphenIsAccepted(t *testing.T) {
	fake := signedOutFake(t)
	if got := fake.run(t, "login", "--profile", "a_1-"+strings.Repeat("b", 28), "--json"); got.exit != exitOK {
		t.Fatalf("a 32-character name with _ and - was refused: %s", got.stdout)
	}
}

func TestLoginNamesTheProfile(t *testing.T) {
	fake := signedOutFake(t)
	fake.profile = "acme"
	if got := fake.run(t, "login"); got.exit != exitOK || got.stdout != "Signed in as dev@example.com (profile acme)\n" {
		t.Fatalf("login: exit %d: %q", got.exit, got.stdout)
	}
}

func TestWhoamiNamesTheProfile(t *testing.T) {
	fake := signedOutFake(t)
	fake.profile = "acme"
	if got := fake.run(t, "login", "--json"); got.exit != exitOK {
		t.Fatalf("login: %s", got.stdout)
	}
	if got := fake.run(t, "whoami"); got.stdout != "Signed in as dev@example.com (profile acme)\n" {
		t.Fatalf("whoami: %q", got.stdout)
	}
}

func TestLogoutNamesTheProfile(t *testing.T) {
	fake := signedOutFake(t)
	fake.profile = "acme"
	if got := fake.run(t, "login", "--json"); got.exit != exitOK {
		t.Fatalf("login: %s", got.stdout)
	}
	if got := fake.run(t, "logout"); got.stdout != "Signed out (profile acme)\n" {
		t.Fatalf("logout: %q", got.stdout)
	}
}

func TestWithNoProfileNoLineNamesOne(t *testing.T) {
	fake := signedOutFake(t)
	if got := fake.run(t, "login"); got.stdout != "Signed in as dev@example.com\n" {
		t.Fatalf("login with no profile: %q", got.stdout)
	}
}

func TestTwoProfilesRefreshAtTheSameMomentWithoutDisturbingEachOther(t *testing.T) {
	fake := signedInFake(t, expired)
	if got := fake.run(t, "login", "--profile", "acme", "--json"); got.exit != exitOK {
		t.Fatalf("login --profile acme: %s", got.stdout)
	}
	acme := readCredentials(t)[fake.server.URL+"#acme"]
	acme.ExpiresAt = time.Now().Add(-time.Second)
	writeCredentialsForTest(t, fake.server.URL+"#acme", acme)
	unnamedChain := fake.chains[readCredentials(t)[fake.server.URL].RefreshToken]
	acmeChain := fake.chains[acme.RefreshToken]
	fake.refreshes = 0
	results := make([]result, 4)
	var wg sync.WaitGroup
	for i := range results {
		args := []string{"whoami", "--json"}
		if i%2 == 1 {
			args = append(args, "--profile", "acme")
		}
		wg.Go(func() { results[i] = fake.run(t, args...) })
	}
	wg.Wait()
	for _, got := range results {
		if got.exit != exitOK {
			t.Fatalf("a concurrent whoami exited %d: %s", got.exit, got.stdout)
		}
	}
	creds := readCredentials(t)
	if fake.refreshes != 2 || len(creds) != 2 || time.Until(creds[fake.server.URL].ExpiresAt) < time.Minute || time.Until(creds[fake.server.URL+"#acme"].ExpiresAt) < time.Minute {
		t.Fatalf("%d refresh grants for two expired sign-ins; credentials %+v", fake.refreshes, creds)
	}
	if unnamedChain == nil || acmeChain == nil || unnamedChain == acmeChain ||
		fake.chains[creds[fake.server.URL].RefreshToken] != unnamedChain || fake.chains[creds[fake.server.URL+"#acme"].RefreshToken] != acmeChain {
		t.Fatalf("a refreshed pair was saved on the other sign-in's token chain; credentials %+v", creds)
	}
}

func TestNotFoundNamesTheProfileTheCommandRanAs(t *testing.T) {
	fake := signedInFake(t, fresh)
	if got := fake.run(t, "login", "--profile", "acme", "--json"); got.exit != exitOK {
		t.Fatalf("login --profile acme: %s", got.stdout)
	}
	named := fake.run(t, "tenants", "list", "--app", "app-gone", "--profile", "acme", "--json")
	assertFailure(t, named, exitRefused, "NOT_FOUND")
	if !strings.Contains(named.stdout, `"message": "no application app-gone that you can reach (signed in with profile \"acme\")"`) {
		t.Fatalf("a NOT_FOUND as profile acme: %s", named.stdout)
	}
	human := fake.run(t, "tenants", "list", "--app", "app-gone", "--profile", "acme")
	if human.stderr != "multipaas: no application app-gone that you can reach (signed in with profile \"acme\") (NOT_FOUND)\n" {
		t.Fatalf("human NOT_FOUND: %q", human.stderr)
	}
}

func TestNotFoundSaysNoProfileWasChosenWhileOthersExist(t *testing.T) {
	fake := signedInFake(t, fresh)
	if got := fake.run(t, "login", "--profile", "acme", "--json"); got.exit != exitOK {
		t.Fatalf("login --profile acme: %s", got.stdout)
	}
	unnamed := fake.run(t, "tenants", "list", "--app", "app-gone", "--json")
	assertFailure(t, unnamed, exitRefused, "NOT_FOUND")
	if !strings.Contains(unnamed.stdout, `"message": "no application app-gone that you can reach (no profile chosen: set MULTIPAAS_PROFILE or pass --profile)"`) {
		t.Fatalf("a NOT_FOUND with no profile while acme exists: %s", unnamed.stdout)
	}
}

func TestNotFoundIsUnchangedWhenNoProfileExists(t *testing.T) {
	fake := signedInFake(t, fresh)
	unchanged := fake.run(t, "tenants", "list", "--app", "app-gone", "--json")
	assertFailure(t, unchanged, exitRefused, "NOT_FOUND")
	if !strings.Contains(unchanged.stdout, `"message": "no application app-gone that you can reach"`) {
		t.Fatalf("with no profiles anywhere the message changed: %s", unchanged.stdout)
	}
}

func TestNotFoundDoesNotWriteTheCredentialStore(t *testing.T) {
	fake := signedInFake(t, fresh)
	home, _ := os.UserHomeDir()
	store := filepath.Join(home, ".config", "multipaas")
	fake.entitlements = fake.entitlements[:1]
	fake.apps["client-a"] = fake.apps["client-a"][:1]
	fake.onGraphQL = func(operation string) {
		if operation == "Applications" {
			_ = os.RemoveAll(store)
		}
	}
	got := fake.run(t, "tenants", "list", "--app", "app-gone", "--json")
	assertFailure(t, got, exitRefused, "NOT_FOUND")
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("computing the hint created the credential store (stat: %v)", err)
	}
}

func TestOnlyNotFoundCarriesTheProfileHint(t *testing.T) {
	fake := signedOutFake(t)
	got := fake.run(t, "whoami", "--profile", "acme", "--json")
	assertFailure(t, got, exitSignedOut, "NOT_SIGNED_IN")
	if strings.Contains(got.stdout, "(signed in with profile") {
		t.Fatalf("a NOT_SIGNED_IN carried the NOT_FOUND hint: %s", got.stdout)
	}
}

func TestSignInTimeoutNamesTheLoginCommandForTheProfile(t *testing.T) {
	defer func(was time.Duration) { signInTimeout = was }(signInTimeout)
	signInTimeout = 100 * time.Millisecond
	idle := func(string) error { return nil }
	fake := signedOutFake(t)
	named := fake.runWith(t, idle, "login", "--profile", "acme", "--json")
	assertFailure(t, named, exitSignedOut, "SIGN_IN_TIMEOUT")
	if !strings.Contains(named.stdout, `sign-in timed out; run multipaas login --profile acme again`) {
		t.Fatalf("a timed-out login to profile acme: %s", named.stdout)
	}
	unnamed := fake.runWith(t, idle, "login", "--json")
	assertFailure(t, unnamed, exitSignedOut, "SIGN_IN_TIMEOUT")
	if !strings.Contains(unnamed.stdout, `sign-in timed out; run multipaas login again`) {
		t.Fatalf("a timed-out login with no profile: %s", unnamed.stdout)
	}
}
