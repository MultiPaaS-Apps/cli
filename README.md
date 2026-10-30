# multipaas

The MultiPaaS command line. It signs you in with your MultiPaaS account. It acts as that account, with that
account's permissions.

## Install

```bash
brew tap multipaas-apps/tap
HOMEBREW_GITHUB_API_TOKEN="$(gh auth token)" brew install multipaas-apps/tap/multipaas
```

While this repository is private, installing needs your GitHub access. See
[the tap's README](https://github.com/MultiPaaS-Apps/homebrew-tap).

## Use

```bash
multipaas login                        # opens your browser
multipaas whoami
multipaas apps list
multipaas apps create --name "Orders"                    # --client <client id> when you can reach more than one
multipaas tenants list --app <app id>
multipaas tenants create --app <app id> --name "Dev test"   # an app-tenant to test in
multipaas model show --app <app id>
multipaas entity create --app <app id> --name Order
multipaas field add --app <app id> --entity <entity id> --type text --name Title --required
multipaas enum create --app <app id> --name Status --values "Open,In progress,Done"
multipaas field add --app <app id> --entity <entity id> --type enum --name Status --enum-type <enum id>
multipaas constraint add --app <app id> --entity <entity id> --fields <field id>,<field id>
multipaas init --app <app id> --tenant <app-tenant id>   # in your app's directory: writes multipaas.json
multipaas versions list
multipaas versions diff                                  # what changed from the pin to the newest version
multipaas pin --api-version <id>
multipaas schema pull                                    # writes multipaas.graphql for your codegen
multipaas records create --entity <entity id> --data '{"<field id>": "value"}'
multipaas records get --entity <entity id> --id <record id>
multipaas records list --entity <entity id> --parent-field <field id> --parent-record <record id>   # a parent's children
multipaas import create --app <app id> --dir ./export          # one import, every .jsonl in ./export added
multipaas import add --import <import id> --kind records --entity <entity id> --file ./export/orders.jsonl
multipaas import check --import <import id>                    # waits, then prints every problem by file and line
multipaas import run --import <import id>                      # only after a clean check; all or nothing
multipaas import status --import <import id> [--include-deleted]
multipaas import delete --import <import id>                   # not while checking or importing; nothing it holds is removed
multipaas oauth-client create --name "My app" --redirect-uri http://localhost:5173/callback
multipaas feedback send --kind bug|feature [--summary S] (--details D | --details-file F) [--app APP_ID | --general]
multipaas feedback list
multipaas feedback show --id ID
multipaas feedback settings [--auto-send on|off]
multipaas logout
```

The model commands are `model show`, `entity create|update|retire`, `enum create|update|retire`, `field add|update|retire` and `constraint add|retire`; `--help` on each lists its flags. Each change prints the changed entity, enum type, field or constraint and `apiVersion`: the API version the model is at after the change.

In a directory with `multipaas.json`, `--app` and `--tenant` default to its `applicationId` and `testAppTenantId`. `init` writes the file and prints the endpoints your app calls. `pin` moves it to another API version, and `schema pull` writes that version's GraphQL schema. `versions diff` shows what changed between two API versions, breaking, dangerous and added, from the pin to the newest unless `--from` and `--to` say otherwise. `records list|get|create|update|retire` read and write an entity's records in an app-tenant, or with `--app-wide` the application's app-wide records (instead of `--tenant`), with `--data` a JSON object keyed by field id. `oauth-client list|create|update` register and change the sign-in client your app's end users use, which needs `application.manage_oauth`. `app-identity list|create|secret add|secret revoke|secret list|retire` manage your app's own identities, which needs `application.manage_oauth`: an app identity's client ID and secret let your own code call the API for a tenant via OAuth client credentials. `apps create` makes an application, which needs `client.applications.create`, and makes you its App manager. `tenants create` makes an app-tenant to test in, which needs `access.manage.application`.

`import create|add|check|run|status|delete` load an application's existing data from export files; see [Import](#import).

`feedback send|list|show|settings` report a bug or feature request to MultiPaaS and follow the status of your reports; see [Feedback](#feedback).

Every command takes `--json`, which prints exactly one JSON document on stdout.

The CLI talks to `https://api.multipaas.com`. To use another MultiPaaS, set `MULTIPAAS_API_URL` to its
OAuth server's origin: `https://api.<host>`, with no port, path, query or fragment. Anything else is refused
with `INVALID_API_URL`. The CLI finds webapp-api by swapping that host's leading `api.` for `webapp-api.`.

| Exit | Meaning |
|---|---|
| 0 | Done |
| 1 | Refused or failed. With `--json`, stdout is `{"error": {"code", "message"}}`, plus `fieldErrors` (each with `field`, `rule` and `message`) on a `VALIDATION` refusal |
| 2 | Not signed in |
| 3 | This CLI is too old for MultiPaaS: upgrade it |

Your sign-in is kept in `~/.config/multipaas/credentials.json` (mode 0600), one entry per API URL, so a token is only ever sent to the API URL it was issued for.

## Import

`import` loads an application's existing data from export files, one [JSON-lines](https://jsonlines.org) file per kind:

| File | Each line |
|---|---|
| `tenants.jsonl` | `{"id": "acct-17", "name": "Acme"}` — one app-tenant |
| `users.jsonl` | `{"id": "u-9", "email": "pat@example.com", "name": "Pat Lee"}` — `name` is optional |
| `memberships.jsonl` | `{"user": "u-9", "tenant": "acct-17", "role": "Editor"}` — a role your application declares |
| `<entity>.jsonl` | `{"id": "s-301", "tenant": "acct-17", "createdBy": "u-9", "createdAt": "2025-03-02T10:00:00Z", "updatedAt": "2025-04-01T08:00:00Z", "deletedAt": null, "fields": {"title": "Q1 review", "parent": "s-300"}}` |

- `id`, `user`, `tenant` and `createdBy` are ids from your old database, and a reference field's value is
  the id of the row it points at. MultiPaaS gives every tenant and record a new id and remaps the
  references. A user whose email already has an account is linked to that account, which keeps its id and name.
- An `id` is at most 1,024 bytes in UTF-8 and has no control characters or unpaired surrogates (problems
  `id-too-long` and `id-invalid-character`).
- `fields` is keyed by each field's GraphQL name. `createdBy`, the three timestamps and `deletedAt` are
  optional; a non-null `deletedAt` imports the record as deleted.
- `create --dir` matches `<entity>.jsonl` to an entity by its name, type name or plural name, ignoring
  case, and refuses the whole directory if any `.jsonl` file matches none or more than one, or if two files
  map to the same kind or the same entity. A `tenants`, `users` or `memberships` file that also matches an
  entity is refused as `IMPORT_FILE_AMBIGUOUS`: add it with `import add`, using `--kind tenants`, `users` or
  `memberships` for the reserved meaning, or `--kind records --entity ENTITY_ID` for the entity. A file that
  cannot be read, is empty or is over 512 MiB is refused as `IMPORT_FILE_INVALID`. Nothing is created
  when any file is refused.

`check` and `run` wait until they finish (`--wait=false` returns at once) and exit 0 whatever the
result: read `status`. They stop waiting after 20 minutes and exit 1 with `IMPORT_STILL_RUNNING`; run
`import status` later. A failed check is `check-failed`, with its problems; a data problem names its file, and its line when it is about a row. A run
writes everything or nothing; it is `imported` with counts, or `import-failed` with problems. Imported users
are not emailed. They join their tenants at their next sign-in within 7 days of the run; after that
they must be invited.

`import delete` marks an import deleted, and is refused with `DATA_IMPORT_STATE` while the import is checking or
importing. Nothing is removed: its files stay, and so does any data it imported. A deleted import can't be
changed, and `import status` finds it only with `--include-deleted`.

With `--json`, `import create`, `check`, `run`, `status` and `delete` print `{"import": {...}}`, and `import add` prints
`{"importFile": {...}}`. An import holds `id`, `status`, `problemCount`, `problems` (each with `file`, `line`,
`sourceId`, `field`, `code` and `message`), `counts` (each a `key` and a `count`), `files`, `startedAt`,
`finishedAt` and `deletedAt` (set once the import is deleted). `add --kind records` needs `--entity`, the entity's id from `model show`.

## Feedback

`feedback send` files a bug report or feature request with MultiPaaS. `--kind` is `bug` or `feature` (the default),
and the details come from `--details`, or from a file with `--details-file F`, or from standard input with
`--details-file -`. `--summary` is a one-line title; without one, `feedback list` and `feedback show` title
the report by the first line of its details.

Every report records the CLI version. Inside a project (a directory with `multipaas.json`) it also records the
application and the API version the project is pinned to. `--app APP_ID` makes the report about another
application, without the pinned version, and `--general` makes it about MultiPaaS in general, recording
neither the application nor your client. `--general` sends no scope, so it works even when the project's
application is no longer yours to reach.

`feedback list` prints your reports, newest first, with their status. `feedback show --id ID` prints one with
its status history. With `--json` they print `[{...}]` and `{"feedback": {...}}`; an id that isn't yours is `NOT_FOUND`.

`feedback settings` prints whether Claude may send your feedback without asking first, and `feedback settings --auto-send on`
(or `off`) changes it. It starts off.

## Develop

The Go version is the one in `go.mod`.

The tap publishes a Formula, not a Cask. The binary is unsigned, and Homebrew quarantines cask downloads
(Homebrew 7.0.9, `Library/Homebrew/cask/download.rb:254-255` calls `Quarantine.cask!`). In
`Library/Homebrew`, outside `cask/`, `extend/`, `os/` and `test/`, `Quarantine` appears only in
`diagnostic.rb:1500` (a support check) and `install_steps.rb:1329` (an App Management check);
`formula_installer.rb` and `build.rb` never mention it.

```bash
go test ./...
```

The typed operations are generated from `operations.graphql` and `schema.graphql`, a copy of webapp-api's schema.
Every CI run regenerates them from the committed `schema.graphql` and fails if `generated.go` is out of date. The
`schema-drift` job, which runs nightly and when run by hand from the Actions tab, compares `schema.graphql` with the
live schema at the repository secret `SCHEMA_ENDPOINT` and fails with the diff when they differ. To regenerate:

```bash
go tool gqlfetch --endpoint <your MultiPaaS's webapp-api /graphql> --without-builtins > schema.graphql
go tool genqlient
```

A plain `go build` reports version `dev`, which MultiPaaS refuses. To try a build, stamp a version: `go build -ldflags "-X main.version=0.1.0-rc.1" -o multipaas .`

Releases are cut by release-please. Merging a `feat:` or `fix:` PR to `main` opens or updates a
release PR with the changelog, and merging that PR tags `vX.Y.Z`, which publishes the binaries and the
Homebrew formula.
