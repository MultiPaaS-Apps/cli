package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAPIPrintsTheServersResponseAndExitsZero(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.rawAnswers = []map[string]any{{"data": map[string]any{"feedbackReport": map[string]any{"id": "r-1"}}}}

	got := fake.run(t, "api", "--query", `{ feedbackReport(id: "r-1") { id } }`)
	if got.exit != exitOK || compact(got.stdout) != `{"data":{"feedbackReport":{"id":"r-1"}}}` {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if len(fake.rawRequests) != 1 || fake.rawRequests[0]["query"] != `{ feedbackReport(id: "r-1") { id } }` || fake.scopes[0] != "" {
		t.Fatalf("sent %v in scopes %q", fake.rawRequests, fake.scopes)
	}
}

func TestAPISendsTheVariablesAndScopeGiven(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.rawAnswers = []map[string]any{{"data": map[string]any{"feedbackReport": nil}}}

	got := fake.run(t, "api", "--query", `query Report($id: ID!) { feedbackReport(id: $id) { id } }`, "--variables", `{"id": "r-1"}`, "--scope", "client-a")
	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if variables, _ := fake.rawRequests[0]["variables"].(map[string]any); variables["id"] != "r-1" || fake.scopes[0] != "client-a" {
		t.Fatalf("sent %v in scopes %q", fake.rawRequests, fake.scopes)
	}
}

func TestAPIReadsTheDocumentFromStandardInput(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.rawAnswers = []map[string]any{{"data": map[string]any{"me": map[string]any{"id": "user-1"}}}}

	got := fake.runWithStdin(t, "{ me { id } }\n", "api", "--query-file", "-")
	if got.exit != exitOK || len(fake.rawRequests) != 1 || fake.rawRequests[0]["query"] != "{ me { id } }\n" {
		t.Fatalf("exit %d, sent %v: %s", got.exit, fake.rawRequests, got.stderr)
	}
}

func TestAPIPrintsAResponseWithErrorsWholeAndExitsOne(t *testing.T) {
	fake := signedInFake(t, fresh)
	answer := map[string]any{
		"data":   nil,
		"errors": []map[string]any{{"message": "status unchanged", "path": []string{"setFeedbackStatus"}, "extensions": map[string]any{"code": "FEEDBACK_STATUS_UNCHANGED"}}},
	}
	fake.rawAnswers = []map[string]any{answer}

	got := fake.run(t, "api", "--query", `mutation { setFeedbackStatus(id: "r-1", status: planned) { id } }`)
	want, _ := json.Marshal(answer)
	if got.exit != exitRefused || compact(got.stdout) != string(want) || got.stderr != "" {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
}

func TestAPIExitsOneForAnErrorStatusWithoutGraphQLErrors(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.rawStatus = 503
	fake.rawAnswers = []map[string]any{{"data": map[string]any{"me": nil}}}

	got := fake.run(t, "api", "--query", "{ me { id } }")
	if got.exit != exitRefused || compact(got.stdout) != `{"data":{"me":null}}` {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
}

func TestAPIExitsOneForABodyWithoutData(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.rawAnswers = []map[string]any{nil, {"message": "Internal Server Error"}}

	null := fake.run(t, "api", "--query", "{ me { id } }")
	message := fake.run(t, "api", "--query", "{ me { id } }")
	if null.exit != exitRefused || compact(null.stdout) != `null` || message.exit != exitRefused || compact(message.stdout) != `{"message":"Internal Server Error"}` {
		t.Fatalf("null: exit %d %s; message: exit %d %s", null.exit, null.stdout, message.exit, message.stdout)
	}
}

func TestAPIExitsOneForDataThatIsNotAnObject(t *testing.T) {
	for _, data := range []any{1, nil, []any{}} {
		fake := signedInFake(t, fresh)
		fake.rawAnswers = []map[string]any{{"data": data}}

		got := fake.run(t, "api", "--query", "{ me { id } }")
		want, _ := json.Marshal(map[string]any{"data": data})
		if got.exit != exitRefused || compact(got.stdout) != string(want) {
			t.Errorf("data %v: exit %d: %s %s", data, got.exit, got.stdout, got.stderr)
		}
	}
}

func TestAPINeverRetriesAFieldsRefusal(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.rawAnswers = []map[string]any{{
		"data":   nil,
		"errors": []map[string]any{{"message": "unauthorized", "path": []string{"setFeedbackStatus"}, "extensions": map[string]any{"code": "UNAUTHORIZED"}}},
	}}

	got := fake.run(t, "api", "--query", `mutation { setFeedbackStatus(id: "r-1", status: planned) { id } }`)
	if got.exit != exitRefused || len(fake.rawRequests) != 1 || fake.refreshes != 0 || !strings.Contains(got.stdout, `"UNAUTHORIZED"`) {
		t.Fatalf("exit %d after %d requests and %d refreshes: %s", got.exit, len(fake.rawRequests), fake.refreshes, got.stdout)
	}
}

func TestAPIRefreshesATokenTheServerRefusesAndRetriesOnce(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.chains[readCredentials(t)[fake.server.URL].AccessToken].deadAccess = true
	fake.rawAnswers = []map[string]any{{"data": map[string]any{"me": map[string]any{"id": "user-1"}}}}

	got := fake.run(t, "api", "--query", "{ me { id } }")
	if got.exit != exitOK || fake.refreshes != 1 || len(fake.scopes) != 2 || len(fake.rawRequests) != 1 {
		t.Fatalf("exit %d after %d refreshes and %d requests: %s", got.exit, fake.refreshes, len(fake.scopes), got.stdout)
	}
}

func TestAPIWithoutSigningInExitsTwoWithoutCallingTheServer(t *testing.T) {
	fake := signedOutFake(t)

	assertFailure(t, fake.run(t, "api", "--query", "{ me { id } }", "--json"), exitSignedOut, "NOT_SIGNED_IN")
	if len(fake.scopes) != 0 {
		t.Fatalf("server called %d times", len(fake.scopes))
	}
}

func TestAPIOnAnOldCLIPrintsTheServersAnswerAndExitsThree(t *testing.T) {
	fake := signedInFake(t, fresh)

	got := fake.runAs(t, "0.0.9", "api", "--query", "{ me { id } }")
	if got.exit != exitUpgrade || !strings.Contains(got.stdout, `"UNSUPPORTED_CLI_VERSION"`) {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestAPINeedsADocument(t *testing.T) {
	fake := signedInFake(t, fresh)

	assertFailure(t, fake.run(t, "api", "--json"), exitRefused, "CLI_ERROR")
	if len(fake.scopes) != 0 {
		t.Fatalf("server called %d times", len(fake.scopes))
	}
}

func TestAPIRefusesVariablesThatAreNotAJSONObject(t *testing.T) {
	fake := signedInFake(t, fresh)

	for _, variables := range []string{`[1]`, `null`, `{"id":`} {
		assertFailure(t, fake.run(t, "api", "--query", "{ me { id } }", "--variables", variables, "--json"), exitRefused, "CLI_ERROR")
	}
	if len(fake.scopes) != 0 {
		t.Fatalf("server called %d times", len(fake.scopes))
	}
}
