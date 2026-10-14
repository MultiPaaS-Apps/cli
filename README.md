# multipaas

The MultiPaaS command line. It signs you in with your MultiPaaS account and runs the same operations
the MultiPaaS web app runs, with the same permissions.

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
multipaas oauth-client create --name "My app" --redirect-uri http://localhost:5173/callback
multipaas logout
```

The model commands are `model show`, `entity create|update|retire`, `enum create|update|retire`, `field add|update|retire` and `constraint add|retire`; `--help` on each lists its flags. Each change prints the changed entity, enum type, field or constraint and `apiVersion`: the API version the model is at after the change.

In a directory with `multipaas.json`, `--app` and `--tenant` default to its `applicationId` and `testAppTenantId`. `init` writes the file and prints the endpoints your app calls. `pin` moves it to another API version, and `schema pull` writes that version's GraphQL schema. `versions diff` shows what changed between two API versions, breaking, dangerous and added, from the pin to the newest unless `--from` and `--to` say otherwise. `records list|create|update|retire` read and write an entity's records in an app-tenant, with `--data` a JSON object keyed by field id. `oauth-client list|create|update` register and change the sign-in client your app's end users use, which needs `application.manage_oauth`. `tenants create` makes an app-tenant to test in, which needs `access.manage.application`.

Every command takes `--json`, which prints exactly one JSON document on stdout.

The CLI talks to `https://api.multipaas.com`. To use another MultiPaaS, set `MULTIPAAS_API_URL` to its
OAuth server's origin: `https://api.<host>`, with no port, path, query or fragment. Anything else is refused
with `INVALID_API_URL`. The CLI finds webapp-api by swapping that host's leading `api.` for `webapp-api.`.

| Exit | Meaning |
|---|---|
| 0 | Done |
| 1 | Refused or failed. With `--json`, stdout is `{"error": {"code", "message"}}` |
| 2 | Not signed in |
| 3 | This CLI is too old for MultiPaaS: upgrade it |

Your sign-in is kept in `~/.config/multipaas/credentials.json` (mode 0600), one entry per API URL, so a token is only ever sent to the API URL it was issued for.

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

The typed operations are generated from `operations.graphql` and the live schema of webapp-api. CI checks
them against the repository secret `SCHEMA_ENDPOINT`:

```bash
go tool gqlfetch --endpoint <your MultiPaaS's webapp-api /graphql> --without-builtins > schema.graphql
go tool genqlient
```

A plain `go build` reports version `dev`, which MultiPaaS refuses. To try a build, stamp a version: `go build -ldflags "-X main.version=0.1.0-rc.1" -o multipaas .`

To release, push a tag `vX.Y.Z` at or above `v0.1.0`. goreleaser publishes the binaries and the
Homebrew formula.
