package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestFetchSchemaKeepsDeprecatedArgumentsAndInputFields(t *testing.T) {
	requireTools(t)
	server := introspectionServer(t)

	stdout, stderr, err := runFetchSchema(server.URL)

	if err != nil {
		t.Fatalf("fetch-schema failed: %v\n%s", err, stderr)
	}
	for _, want := range []string{"includeDeleted: Boolean", "includeRetired: Boolean", "name: String", "oldName: String"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("schema is missing %q:\n%s", want, stdout)
		}
	}
}

func TestFetchSchemaKeepsDeprecatedDefaultedInputsOptional(t *testing.T) {
	requireTools(t)
	server := introspectionServer(t)

	stdout, stderr, err := runFetchSchema(server.URL)

	if err != nil {
		t.Fatalf("fetch-schema failed: %v\n%s", err, stderr)
	}
	for _, want := range []string{"legacyLimit: Int = 10 @deprecated", `legacyTag: String = "x"@deprecated`, "includeRetired: Boolean @deprecated", "oldName: String @deprecated"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("schema is missing %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"legacyLimit: Int!", "legacyTag: String!"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("schema still has %q:\n%s", unwanted, stdout)
		}
	}
	schema, gqlErr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: stdout})
	if gqlErr != nil {
		t.Fatalf("the printed schema does not load: %v", gqlErr)
	}
	_, queryErrs := gqlparser.LoadQuery(schema, `query { items(filter: {name: "a"}) }`)
	if len(queryErrs) != 0 {
		t.Errorf("a query that leaves out the deprecated inputs is invalid: %v", queryErrs)
	}
}

func TestFetchSchemaKeepsDefaultValues(t *testing.T) {
	requireTools(t)
	server := introspectionServer(t)

	stdout, stderr, err := runFetchSchema(server.URL)

	if err != nil {
		t.Fatalf("fetch-schema failed: %v\n%s", err, stderr)
	}
	for _, want := range []string{"page: PageInput! = {page:0,pageSize:20}", "direction: SortDirection! = ASC", "page: Int! = 0", "pageSize: Int! = 20"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("schema is missing %q:\n%s", want, stdout)
		}
	}
	schema, gqlErr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: stdout})
	if gqlErr != nil {
		t.Fatalf("the printed schema does not load: %v", gqlErr)
	}
	_, queryErrs := gqlparser.LoadQuery(schema, `query { pages }`)
	if len(queryErrs) != 0 {
		t.Errorf("a query that leaves out the defaulted arguments is invalid: %v", queryErrs)
	}
}

func TestFetchSchemaKeepsArgumentAndInputFieldDeprecations(t *testing.T) {
	requireTools(t)
	server := introspectionServer(t)

	stdout, stderr, err := runFetchSchema(server.URL)

	if err != nil {
		t.Fatalf("fetch-schema failed: %v\n%s", err, stderr)
	}
	for _, want := range []string{`includeRetired: Boolean @deprecated(reason: "use includeDeleted")`, `oldName: String @deprecated(reason: "use name")`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("schema is missing %q:\n%s", want, stdout)
		}
	}
}

func TestFetchSchemaKeepsEachFieldsOwnDescription(t *testing.T) {
	requireTools(t)
	server := introspectionServer(t)

	stdout, stderr, err := runFetchSchema(server.URL)

	if err != nil {
		t.Fatalf("fetch-schema failed: %v\n%s", err, stderr)
	}
	schema, gqlErr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: stdout})
	if gqlErr != nil {
		t.Fatalf("the printed schema does not load: %v", gqlErr)
	}
	for _, tc := range []struct{ typ, field, want string }{
		{"PageInput", "page", "The page, from 0."},
		{"PageInput", "pageSize", ""},
		{"Node", "id", "The node's id."},
	} {
		if got := schema.Types[tc.typ].Fields.ForName(tc.field).Description; got != tc.want {
			t.Errorf("%s.%s description = %q, want %q", tc.typ, tc.field, got, tc.want)
		}
	}
	if got := schema.Types["PageInput"].Description; got != "Paging for a list." {
		t.Errorf("PageInput description = %q, want %q", got, "Paging for a list.")
	}
}

func TestFetchSchemaFailsOnGraphQLErrors(t *testing.T) {
	requireTools(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
	}))
	t.Cleanup(server.Close)

	_, stderr, err := runFetchSchema(server.URL)

	if err == nil {
		t.Fatal("fetch-schema exited 0 on a GraphQL errors response")
	}
	for _, want := range []string{"fetch-schema: the introspection query failed", "boom"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q: %q", want, stderr)
		}
	}
}

func TestFetchSchemaFailsOnNullData(t *testing.T) {
	requireTools(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(server.Close)

	stdout, stderr, err := runFetchSchema(server.URL)

	if err == nil {
		t.Fatalf("fetch-schema exited 0 on a null data response; stdout %q", stdout)
	}
	if !strings.Contains(stderr, "fetch-schema: the response has no data.__schema object") {
		t.Errorf("stderr does not carry the script's message: %q", stderr)
	}
}

// fieldArgsSelection matches a field selection whose args ask for deprecated ones; directive args are a
// different selection and do not satisfy it.
var fieldArgsSelection = regexp.MustCompile(`fields\(includeDeprecated: true\)\s*\{[^{}]*args\(includeDeprecated: true\)`)

// introspectionServer serves testdata/introspection.json the way the spec defines introspection: deprecated
// arguments and input fields are left out unless the query asks for them with includeDeprecated: true.
func introspectionServer(t *testing.T) *httptest.Server {
	t.Helper()
	fixture, err := os.ReadFile("testdata/introspection.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var doc map[string]any
		if err := json.Unmarshal(fixture, &doc); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		keepArgs := fieldArgsSelection.MatchString(req.Query)
		keepInputFields := strings.Contains(req.Query, "inputFields(includeDeprecated: true)")
		schema := doc["data"].(map[string]any)["__schema"].(map[string]any)
		for _, typ := range schema["types"].([]any) {
			typ := typ.(map[string]any)
			if fields, ok := typ["fields"].([]any); ok && !keepArgs {
				for _, field := range fields {
					field := field.(map[string]any)
					field["args"] = withoutDeprecated(field["args"])
				}
			}
			if !keepInputFields {
				typ["inputFields"] = withoutDeprecated(typ["inputFields"])
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(server.Close)
	return server
}

func withoutDeprecated(values any) any {
	list, ok := values.([]any)
	if !ok {
		return values
	}
	kept := []any{}
	for _, value := range list {
		if value.(map[string]any)["isDeprecated"] != true {
			kept = append(kept, value)
		}
	}
	return kept
}

func runFetchSchema(endpoint string) (stdout, stderr string, err error) {
	cmd := exec.Command("scripts/fetch-schema", endpoint)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bash", "curl", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}
