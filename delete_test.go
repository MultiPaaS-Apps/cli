package main

import (
	"slices"
	"strings"
	"testing"
)

// deleteCase is one command group that offers delete and keeps retire as its deprecated name.
type deleteCase struct {
	group      string
	args       []string // after the verb
	prepare    func(*fakeMultiPaaS, *testing.T)
	deleteOp   string
	retireOp   string
	deleteJSON string
	retireJSON string
}

func deleteCases() []deleteCase {
	record := map[string]any{"id": "record-9", "version": "v1", "appWide": false, "data": map[string]any{"field-1": "a"}, "createdAt": "x", "updatedAt": "x"}
	return []deleteCase{
		{group: "entity", args: []string{"--app", "app-a-000", "--id", "entity-1", "--json"},
			deleteOp: "DeleteEntity", retireOp: "RetireEntity", deleteJSON: entityJSON, retireJSON: entityJSON},
		{group: "enum", args: []string{"--app", "app-a-000", "--id", "enum-1", "--json"},
			deleteOp: "DeleteEnumType", retireOp: "RetireEnumType", deleteJSON: enumJSON, retireJSON: enumJSON},
		{group: "field", args: []string{"--app", "app-a-000", "--id", "field-1", "--json"},
			deleteOp: "DeleteField", retireOp: "RetireField", deleteJSON: fieldJSON, retireJSON: fieldJSON},
		{group: "constraint", args: []string{"--app", "app-a-000", "--id", "constraint-1", "--json"},
			deleteOp: "DeleteConstraint", retireOp: "RetireConstraint", deleteJSON: constraintJSON, retireJSON: constraintJSON},
		{group: "records", args: []string{"--app", "app-a-000", "--tenant", "tenant-a", "--entity", "entity-1", "--id", "record-9", "--json"},
			prepare: func(fake *fakeMultiPaaS, _ *testing.T) {
				fake.records["entity-1"] = []map[string]any{record}
			},
			deleteOp: "DeleteRecord", retireOp: "RetireRecord", deleteJSON: recordJSON, retireJSON: recordJSON},
		{group: "deployers", args: []string{"--app", "app-a-000", "--id", "deployer-1", "--json"},
			prepare: func(fake *fakeMultiPaaS, t *testing.T) {
				fake.run(t, "deployers", "create", "--app", "app-a-000", "--name", "CI", "--json")
			},
			deleteOp: "DeleteDeployer", retireOp: "RetireDeployer", deleteJSON: `{"deleted":true,"id":"deployer-1"}`, retireJSON: `{"id":"deployer-1","retired":true}`},
		{group: "app-identity", args: []string{"app-identity-1", "--app", "app-a-000", "--json"},
			prepare: func(fake *fakeMultiPaaS, t *testing.T) {
				fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
			},
			deleteOp: "DeleteAppIdentity", retireOp: "RetireAppIdentity", deleteJSON: appIdentityJSON, retireJSON: appIdentityJSON},
	}
}

const (
	entityJSON      = `{"apiVersion":"rev-1","entity":{"id":"entity-1","name":"","graphqlTypeName":"","graphqlPluralName":"","managed":false,"shareNewRecordsWithTenant":null}}`
	enumJSON        = `{"apiVersion":"rev-1","enumType":{"id":"enum-1","name":"","graphqlTypeName":"","values":null}}`
	fieldJSON       = `{"apiVersion":"rev-1","field":{"id":"field-1","entity":{"id":""},"name":null,"graphqlFieldName":"","type":"","maxFileBytes":null,"required":false,"notNull":false,"list":false,"defaultValue":null,"enumValues":null,"enumType":null,"isLifecycle":false,"isParent":false,"referencesEntity":null,"relatedField":null}}`
	constraintJSON  = `{"apiVersion":"rev-1","constraint":{"id":"constraint-1","entity":{"id":""},"kind":"","fields":{"items":null}}}`
	recordJSON      = `{"record":{"id":"record-9","appWide":false,"data":{"field-1":"a"},"createdAt":"x","updatedAt":"x","version":"v1","files":[]}}`
	appIdentityJSON = `{"appIdentity":{"id":"app-identity-1","name":"nightly sync","environment":{"id":"env-default"},"user":{"id":"service-user-1"},"createdAt":"2026-10-27T00:00:00.000Z","secrets":[{"id":"secret-1","lastFour":"0001","createdAt":"2026-10-27T00:00:00.000Z","lastUsedAt":null}]}}`
)

func TestDeleteCommandsSendTheDeleteOperation(t *testing.T) {
	for _, c := range deleteCases() {
		t.Run(c.group, func(t *testing.T) {
			fake := signedInFake(t, fresh)
			if c.prepare != nil {
				c.prepare(fake, t)
			}
			fake.operations = nil
			got := fake.run(t, append([]string{c.group, "delete"}, c.args...)...)
			if got.exit != exitOK || compact(got.stdout) != c.deleteJSON {
				t.Fatalf("exit %d stdout %s, want %s", got.exit, compact(got.stdout), c.deleteJSON)
			}
			if got.stderr != "" {
				t.Fatalf("stderr %q", got.stderr)
			}
			if !slices.Contains(fake.operations, c.deleteOp) || slices.Contains(fake.operations, c.retireOp) {
				t.Fatalf("sent %v, want %s and not %s", fake.operations, c.deleteOp, c.retireOp)
			}
		})
	}
}

func TestRetireCommandsAreDeprecatedButUnchanged(t *testing.T) {
	for _, c := range deleteCases() {
		t.Run(c.group, func(t *testing.T) {
			fake := signedInFake(t, fresh)
			if c.prepare != nil {
				c.prepare(fake, t)
			}
			fake.operations = nil
			got := fake.run(t, append([]string{c.group, "retire"}, c.args...)...)
			if got.exit != exitOK || compact(got.stdout) != c.retireJSON {
				t.Fatalf("exit %d stdout %s, want %s", got.exit, compact(got.stdout), c.retireJSON)
			}
			if want := `"` + c.group + ` retire" is deprecated: use "` + c.group + ` delete" instead` + "\n"; got.stderr != want {
				t.Fatalf("stderr %q, want %q", got.stderr, want)
			}
			if !slices.Contains(fake.operations, c.retireOp) || slices.Contains(fake.operations, c.deleteOp) {
				t.Fatalf("sent %v, want %s and not %s", fake.operations, c.retireOp, c.deleteOp)
			}
			help := fake.run(t, c.group, "--help")
			if strings.Contains(help.stdout, "retire") || !strings.Contains(help.stdout, "delete") {
				t.Fatalf("help %q", help.stdout)
			}
		})
	}
}

func TestDeleteOfADeployerAndAnAppIdentityPrintTheirNewText(t *testing.T) {
	fake := signedInFake(t, fresh)
	fake.run(t, "deployers", "create", "--app", "app-a-000", "--name", "CI", "--json")
	fake.run(t, "app-identity", "create", "--app", "app-a-000", "--name", "nightly sync", "--json")
	if got := fake.run(t, "deployers", "delete", "--app", "app-a-000", "--id", "deployer-1"); got.stdout != "Deleted deployer deployer-1\n" {
		t.Fatalf("deployers delete %d %q", got.exit, got.stdout)
	}
	if got := fake.run(t, "app-identity", "delete", "app-identity-1", "--app", "app-a-000"); got.stdout != "Deleted nightly sync; its tokens stop working now.\n" {
		t.Fatalf("app-identity delete %d %q", got.exit, got.stdout)
	}
}
