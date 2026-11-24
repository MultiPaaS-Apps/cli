# multipaas

The MultiPaaS command line. It signs you in with your MultiPaaS account. It acts as that account, with that
account's permissions.

## Install

```bash
brew install multipaas-apps/tap/multipaas
```

## Use

```bash
multipaas login                        # opens your browser
multipaas whoami
multipaas login --profile acme         # a second sign-in, kept beside the first
multipaas apps list
multipaas apps create --name "Orders"                    # --client <client id> when you can reach more than one
multipaas apps delete <app id>                           # its app-tenants and their members go with it; its data is destroyed 30 days later
multipaas tenants list --app <app id>
multipaas tenants create --app <app id> --name "Dev test"   # an app-tenant to test in
multipaas tenants invite --app <app id> --tenant <app-tenant id> --email person@example.com --role <role key> --role <role key>   # emails them a link; they hold the roles once they accept
multipaas tenants members --app <app id> --tenant <app-tenant id>   # its members, without their roles, and its pending invitations
multipaas tenants invitation update --app <app id> --tenant <app-tenant id> --invitation <invitation id> --role <role key>   # the roles given replace the invitation's
multipaas model show --app <app id>
multipaas entity create --app <app id> --name Order
multipaas field add --app <app id> --entity <entity id> --type text --name Title --required
multipaas enum create --app <app id> --name Status --values "Open,In progress,Done"
multipaas field add --app <app id> --entity <entity id> --type enum --name Status --enum-type <enum id>
multipaas field add --app <app id> --entity <entity id> --type number --name Priority --default 3 --not-null
multipaas field add --app <app id> --entity <entity id> --type text --name Tags --list --default '["a"]'   # a text or enum field that holds a list of values
multipaas constraint add --app <app id> --entity <entity id> --fields <field id>,<field id>
multipaas init --app <app id> --tenant <app-tenant id>   # in your app's directory: writes multipaas.json
multipaas versions list
multipaas versions diff                                  # what changed from the pin to the newest version
multipaas pin --api-version <id>
multipaas schema pull                                    # writes multipaas.graphql for your codegen
multipaas records create --entity <entity id> --data '{"<field id>": "value"}'
multipaas records upload --entity <entity id> --field <field id> ./photo.png   # prints the file id to set with --data
multipaas records get --entity <entity id> --id <record id>
multipaas records list --entity <entity id> --parent-field <field id> --parent-record <record id>   # a parent's children
multipaas records list --entity <entity id> --where '{"<field id>": {"lte": 5}}' --order-by <field id>:desc   # matching records, in order
multipaas records list --entity <entity id> --count   # how many
multipaas records list --entity <entity id> --aggregate <number field id>   # its sum, min and max
multipaas import create --app <app id> --env <env id> --dir ./export          # one import, every .jsonl in ./export added
multipaas import add --app <app id> --env <env id> --import <import id> --kind records --entity <entity id> --file ./export/orders.jsonl
multipaas import check --app <app id> --env <env id> --import <import id>                    # waits, then prints every problem by file and line
multipaas import run --app <app id> --env <env id> --import <import id>                      # only after a clean check; all or nothing
multipaas import status --app <app id> --env <env id> --import <import id> [--include-deleted]
multipaas import invitations --app <app id> --env <env id> --import <import id>              # the lapsed ones; --tenant <app-tenant id> reaches imports run before they recorded theirs
multipaas import reissue --app <app id> --env <env id> --import <import id>                  # re-issued ones are claimed at the next sign-in within 7 days; --tenant reaches a deleted import's and re-issues every import's lapsed ones in that app-tenant
multipaas import delete --app <app id> --env <env id> --import <import id>                   # not while checking or importing; nothing it holds is removed
multipaas oauth-client create --name "My app" --redirect-uri http://localhost:5173/callback
multipaas deployers create --app <app id> --name "CI"      # prints the deployer's client ID and secret, once
multipaas deployers list --app <app id>
multipaas deployers delete --app <app id> --id <client id>
MULTIPAAS_CLIENT_ID=<client id> MULTIPAAS_CLIENT_SECRET=<secret> multipaas envs create --app <app id> --name pr-42   # in CI, as the deployer
multipaas envs create --app <app id> --name pr-42 --sign-in-client <client id> --redirect-uri https://pr42.example.com/callback
multipaas envs redirect-uris set --app <app id> --env <env id> --sign-in-client <client id> --redirect-uri https://pr42.example.com/callback
multipaas envs redirect-uris list --app <app id> --env <env id>
multipaas feedback send --kind bug|feature [--summary S] (--details D | --details-file F) [--app APP_ID | --general]
multipaas feedback list
multipaas feedback show --id ID
multipaas feedback settings [--auto-send on|off]
multipaas logout
multipaas api --query '{ me { email } }'                  # any GraphQL, as you
```

As a deployer, `tenants invite`, `tenants members` and `tenants invitation update` work for an app-tenant in an environment that deployer created, and only there. The deployer can give only roles it could give an app identity, so never one carrying `model.edit` or `environment.manage`, and can't invite into a group or change an invitation that carries one. An app-tenant elsewhere, the default environment's included, is refused with `NOT_FOUND`, and a role it can't give with `UNAUTHORIZED`.

`url-name show|rename` show and change the URL names your sites' hostnames are built from: the app's, an environment's with `--env`, or the app's client's with `--client`. Renaming the app needs `environment.manage` in every environment, an environment needs `environment.manage` there, and the client needs `client.access.manage` in the client, or the operator permission `platform.clients.manage`. A client's can change once a month, and its old name stays held for it. Taking a name another app or environment used before needs `--confirm-takeover`. `envs list` shows each environment's site URL, which works before anything is published there.

The model commands are `model show`, `entity create|update|delete`, `enum create|update|delete`, `field add|update|delete` and `constraint add|delete`; `--help` on each lists its flags. `field add --list` makes a text or enum field hold a list of values, chosen when the field is added and never changed; its `--default` is then a JSON array such as `'["a","b"]'`, and `model show` marks it `list-of-text` or `list-of-enum`. Each change prints the changed entity, enum type, field or constraint and `apiVersion`: the API version the model is at after the change.

In a directory with `multipaas.json`, `--app` and `--tenant` default to its `applicationId` and `testAppTenantId`, and `--env` to its `environmentId` on the commands whose `--help` names that default. `init` writes the file and prints the endpoints your app calls. `pin` moves it to another API version, and `schema pull` writes that version's GraphQL schema. `versions diff` shows what changed between two API versions, breaking, dangerous and added, from the pin to the newest unless `--from` and `--to` say otherwise. `records list|get|create|update|delete` read and write an entity's records in an app-tenant, or with `--app-wide` the application's app-wide records (instead of `--tenant`), with `--data` a JSON object keyed by field id. On the `records` and `app-identity role` commands `--env` is given only with `--app-wide`: without `--app-wide` it is refused, with or without `--tenant`, because an app-tenant already belongs to one environment. `records upload` sends a file for a file field and prints its id, which `records create` or `update` then sets as that field's value in `--data`; `records get` and `list` show each file's name, size and a link that works for 15 minutes. `records list` takes `--where`, a JSON object of conditions keyed by field id, or `id`, `createdAt` or `updatedAt`, `--order-by` one field, `--count` to print only how many match, and `--aggregate FIELD_ID` (repeatable) to print the sum (number fields only), min and max of a number or timestamp field, `createdAt` or `updatedAt` over every match that stores a value of the field's type. `records update --expected-version` updates only if the record is still at that version, its `version` from `records get` or `list`, and otherwise is refused with `CONFLICT`. `oauth-client list|create|update` register and change the sign-in client your app's end users use, which needs `application.manage_oauth`. `app-identity list|create|secret add|secret revoke|secret list|delete` manage your app's own identities, which needs `application.manage_oauth`: an app identity's client ID and secret let your own code call the API for a tenant via OAuth client credentials. `deployers create|list|delete` manage the credentials CI uses, which needs `access.manage.application`. With `MULTIPAAS_CLIENT_ID` and `MULTIPAAS_CLIENT_SECRET` set, the CLI acts as that deployer instead of your sign-in, and never reads or writes your saved sign-in. `envs redirect-uris set|list` manage the callback URLs a sign-in client accepts in one environment, on top of the client's own; `set` without `--redirect-uri` clears them. A deployer can create environments and delete only those it created, set their sign-in callback URLs, and in them create and delete app-tenants, create app identities, and give those app identities roles without `model.edit`, and invite people as described above. It can't change the model or reach any other environment. `login` and `logout` are refused while the two are set. `apps create` makes an application, which needs `client.applications.create`, and makes you its App manager. `apps delete` deletes one, which needs `access.manage.application` in every environment: its app-tenants and their members are deleted with it, and its data is destroyed 30 days later, on `destroysAt`. `tenants create` makes an app-tenant to test in, which needs `access.manage.application`. `tenants delete` deletes one, which needs the same permission: its members and every token for it stop working at once, and its records, files and members are destroyed 30 days later, on `destroysAt`. `tenants list --include-deleted` lists deleted ones too, each with `deletedAt` and `destroysAt`. `tenants invite --email EMAIL --role ROLE` invites a person to an app-tenant: `--role` is a role's key or id from `roles list --tenant`, repeated for each role. MultiPaaS emails them a link that works for 7 days, and they hold the roles once they accept it. `tenants members` lists the app-tenant's members, without their roles, and its pending invitations, email and import alike. `tenants invitation update --invitation ID --role ROLE` sets the roles of a pending email invitation that `tenants members` lists: the roles given replace the ones it had, and any groups it has, set in the web app, stay. Inviting and changing an invitation need access management (`access.manage`) in the app-tenant, and every role given must carry only permissions you hold there. `tenants invite` is refused with `UNAUTHORIZED` for a lack of either, and for an app-tenant you can't reach, so the CLI's message asks you to check all three. `tenants invitation update` can't see an invitation without access management, or one that lists a role or group carrying a permission you don't hold there, and refuses with `NOT_FOUND`: it never sends the update, so nothing changes. The server refuses with `UNAUTHORIZED` a role given that carries a permission you don't hold, and an app-tenant you can't reach. When the CLI itself finds `--tenant` is one you can't reach, any of the three commands refuses with `NOT_FOUND` "no app-tenant … that you can reach": it never sends the command's own request, so nothing changes. `tenants members` lists only the invitations you could resend, so without access management it lists none, and with it, only those whose roles and groups carry only permissions you hold there. `tenants retire` and `--include-retired` are the old names; they still work and print a deprecation notice. Likewise `entity retire`, `enum retire`, `field retire`, `constraint retire`, `records retire`, `deployers retire` and `app-identity retire` are the deprecated names of the matching `delete` commands, hidden from `--help`.

`import create|add|check|run|status|invitations|reissue|delete` load an application's existing data from export files; see [Import](#import).

`feedback send|list|show|settings` report a bug or feature request to MultiPaaS and follow the status of your reports; see [Feedback](#feedback).

Every command takes `--json`, which prints exactly one JSON document on stdout, and `--profile` (see [Profiles](#profiles)).

The CLI talks to `https://api.multipaas.com`. To use another MultiPaaS, set `MULTIPAAS_API_URL` to its
OAuth server's origin: `https://api.<host>`, with no port, path, query or fragment. Anything else is refused
with `INVALID_API_URL`. The CLI finds webapp-api by swapping that host's leading `api.` for `webapp-api.`.

| Exit | Meaning |
|---|---|
| 0 | Done |
| 1 | Refused or failed. With `--json`, stdout is `{"error": {"code", "message"}}`, plus `fieldErrors` (each with `field`, `rule` and `message`) on a `VALIDATION` refusal |
| 2 | Not signed in |
| 3 | This CLI is too old for MultiPaaS: upgrade it |

Your sign-in is kept in `~/.config/multipaas/credentials.json` (mode 0600), one entry per API URL and profile, so a token is only ever sent to the API URL it was issued for.

### Profiles

To be signed in as more than one developer on the same MultiPaaS, say one per client you work for, give each sign-in a profile name: it starts with a lowercase letter or digit, then lowercase letters, digits, `-` or `_`, up to 32 characters in all. Every command uses the profile `--profile` names, else the one `MULTIPAAS_PROFILE` names, else your unnamed sign-in, which is the one you had before profiles. Any other name is refused with `INVALID_PROFILE`.

```bash
export MULTIPAAS_PROFILE=acme   # in this shell, or in a gitignored .env.local you source
multipaas login                 # signs in as acme's developer, beside your other sign-ins
multipaas whoami                # Signed in as you@acme.example (profile acme)
multipaas apps list --profile globex   # one command as another profile
```

Each shell, and any agent started from it, uses its own profile, so two shells can work as two clients' developers at once. `login`, `whoami` and `logout` act on the chosen profile only and name it, with `"profile"` in their `--json` output (`null` for the unnamed sign-in). A command that needs a sign-in, such as `whoami`, exits 2 when you haven't signed in to the profile, and the error names the login command to run: `multipaas login`, or `multipaas login --profile NAME` for a named profile. When an application, tenant or record can't be found, the error says which profile the command ran as, or, if none was chosen while others exist, that none was.

## Import

`import` loads an application's existing data from export files, one [JSON-lines](https://jsonlines.org) file per kind:

| File | Each line |
|---|---|
| `tenants.jsonl` | `{"id": "acct-17", "name": "Acme"}` — one new app-tenant, or `{"id": "acct-17", "appTenant": "APP_TENANT_ID"}` to import into an app-tenant that already exists |
| `users.jsonl` | `{"id": "u-9", "email": "pat@example.com", "name": "Pat Lee"}` — `name` is optional |
| `memberships.jsonl` | `{"user": "u-9", "tenant": "acct-17", "role": "Editor"}` — a role your application declares |
| `<entity>.jsonl` | `{"id": "s-301", "tenant": "acct-17", "createdBy": "u-9", "createdAt": "2025-03-02T10:00:00Z", "updatedAt": "2025-04-01T08:00:00Z", "deletedAt": null, "fields": {"title": "Q1 review", "parent": "s-300"}}` |

- `id`, `user`, `tenant` and `createdBy` are ids from your old database, one id names one row of its entity, across every tenant and the app-wide rows, and a reference field's value is
  the id of the row it points at. MultiPaaS gives every tenant and record a new id (or, for an `appTenant` row, uses that app-tenant) and remaps the
  references. A user whose email already has an account is linked to that account, which keeps its id and name. A user row whose old id an earlier import already linked is linked again when its email is that account's, so a later import can repeat `users.jsonl`; under a different email, or once that account is deleted, it is `already-imported`.
- A `tenants.jsonl` row with `appTenant` in place of `name` maps the old tenant id onto that app-tenant: an `id` from `multipaas tenants list` or `tenants create`, live and in the import's environment (problem `unknown-app-tenant` otherwise). No app-tenant is created, and the import's records and memberships for that id go into it, as do those of a later import that has no tenants row for that id, while that app-tenant is live and until an earlier import has put the same old id into a second live app-tenant (then see `ambiguous-tenant` below). A row with `name` whose old tenant id an earlier import created or mapped onto a live app-tenant is still `already-imported`; once every app-tenant that old id went into is deleted, the row creates a new one, which gets none of the earlier records or members (see the `unknown-tenant` remedy in the [Import existing data guide](https://multipaas.com/docs/guides/import-existing-data/)). A row with `appTenant` is never `already-imported`: onto the app-tenant an earlier import already mapped that old id to, or made from it, it selects that app-tenant and records nothing new, and onto another app-tenant it puts the same source into a second one (see below). An app-tenant takes one old tenant id: a row naming an app-tenant that another row of the import, or an earlier import, already holds under a different old id is refused (problem `duplicate-app-tenant`). If another row of this import holds it, keep that first row, delete the refused row, and change every records and memberships row that names the refused row's old id to name the first row's. If an earlier import holds it (created or mapped), delete the refused row and change every row that names its old id to name the earlier import's old id, which needs no tenants row in this import unless that old id is in more than one live app-tenant; then add `{"id": "EARLIER_OLD_ID", "appTenant": "APP_TENANT_ID"}` naming the app-tenant that holds it (see `ambiguous-tenant` below). Leave out a membership row the earlier import already gave unless you mean to grant it again (see the repeated-membership rule in the bullet below that begins "A record's old id is unique per app-tenant"; that rule also says how to renew a lapsed earlier invitation). Either way the rewrite puts both old tenants' data in one app-tenant. Delete any memberships row it makes a repeat of another (a user with the same role in both would otherwise get problem `duplicate-membership`; a row with a different role stays), then make a new import with the edited files (`import create`). If the new import's check reports `duplicate-unique-key`, records share a unique constraint's values: two lines of this import, or a line and a record already in the app-tenant (one an earlier import made, or one no import made). Decide which record to keep, then take one of these:
  - **Keep both.** Change one line's unique-constraint values so it no longer clashes.
  - **Keep the other record and drop this line.** First change every reference field that points at this line's entity and names its old id, in every line that resolves to this record, so it names the other record's old id. A reference resolves in its line's own app-tenant first, then app-wide, as the bullet below that begins "A record's old id is unique per app-tenant" says: for a tenant record that is the lines of its app-tenant, and for an app-wide record the app-wide lines and the tenant lines whose own app-tenant has no record of that entity with that old id. Otherwise those lines get `broken-reference`. A field that points at another entity and holds the same string names a different record: leave it. This works when the other record has an old id: a line of this import, or an earlier import's record, which is named by its old id while it is live. To merge instead of dropping, copy the values you want onto the other record: into its line when it is a line of this import, or with `multipaas records update --app APP_ID --tenant APP_TENANT_ID --entity ENTITY_ID --id RECORD_ID --data 'JSON'` (`--data` is keyed by field id, from `multipaas model show`, not by the names the import lines use) when it is already in the app-tenant. Through `records update` a reference value is the referenced record's MultiPaaS id, not an old id; when that record comes from this import, set it after the import runs. That update is made outside the import, so it stays even if the import's check fails or it is not run.
  - **Keep this line over a record already in the app-tenant.** This applies whether an earlier import made that record or no import did. In order:
    1. Change that record's unique-constraint values with `multipaas records update --app APP_ID --tenant APP_TENANT_ID --entity ENTITY_ID --id RECORD_ID --data 'JSON'` so it no longer clashes.
    2. Make the new import with this line as it is, then check it and run it. Go on to step 3 only once it has run. If you will replace the old record, then right after the import runs, on a managed entity: if the new record is shared with everyone in the app-tenant and the old record wasn't, have the new record's manager remove that share on its **Sharing** tab in the web app, so no one gains access the old record didn't give them.
    3. To replace the old record rather than keep both: if you didn't remove the new record's extra everyone share in step 2, do that first, as step 2 describes. Then, in order:
       1. Wait until this import has run and, when you planned more imports after it, until the last of them has run: until then, their lines that name the old record still resolve to it. When a later import replaces this new record in turn, finish all four of these sub-steps for this record before starting them for that one.
       2. Find the new record's MultiPaaS id with `multipaas records list --app APP_ID --tenant APP_TENANT_ID --entity ENTITY_ID`, change each record that references the old one, including records these imports made, to name that id with `multipaas records update --app APP_ID --tenant APP_TENANT_ID --entity ENTITY_ID --id RECORD_ID --data 'JSON'`.
       3. On a managed entity, the new record is managed by the user its line names, or by whoever ran the import when the line names no one, and has none of the old record's shares: before deleting the old record, see who it is shared with on its **Sharing** tab in the web app, and have the new record's manager share it with them the same way.
       4. Delete the old record with `multipaas records delete --app APP_ID --tenant APP_TENANT_ID --entity ENTITY_ID --id RECORD_ID` (CLI 1.9.0 or later; on an older CLI use `records retire`). Deleting is destructive.

       Any import you make after that names the new record by this line's old id, not the old record's.

    These record changes are made outside the import, so they stay even if the import's check fails or it is not run: fix the reported problem and make a new import again.
  - **Drop this line when the other record was made by no import.** That record has no old id, so no line can name it. Instead, clear the reference fields that point at this line in the other lines. Set each to `null`, never `""` (`broken-reference`). You can also leave a field out of `fields` when it has no default; a field with a default that is left out gets the default record. After the import, set those references through the API. A required reference can't be cleared (`missing-field`): for one, keep both records or keep this line. A parent field of an unmanaged entity can be cleared, but then the record and the records under it can be read only by roles that read every record of their entity or of one above it. To merge into that record instead, copy the values you want onto it with `records update` as above, then clear the references as here.

  Then, except in the keep-this-line option, which makes its import in its step 2, make a new import with the edited files (`import create`).
- To import into an app-tenant that already exists, go in this order. First find it, or create it, in the import's environment: `multipaas tenants list --app APP_ID --env ENV_ID --json` or `multipaas tenants create --app APP_ID --env ENV_ID --name NAME --json` (it prints `{"appTenant": {"id", "name"}}`). Then point your app's config at it, before the import: its deploy settings and app identities, and `testAppTenantId` in `multipaas.json` when it is the app-tenant you test in. Then write the `appTenant` row and run the import.
- A record's old id is unique per app-tenant, an app-wide record's old id is unique app-wide, and a user is held per environment and linked again when its email is unchanged. So one old tenant's data can be imported into a second app-tenant with its original ids. Use the app-tenant you already have for it, as the bullet above says, or create one (`multipaas tenants create --app APP_ID --env ENV_ID --name NAME --json`), then make an import with one `tenants.jsonl` row for that old tenant, `{"id": "OLD_TENANT_ID", "appTenant": "APP_TENANT_ID"}`, and no row for any other old tenant; the record and membership lines of that tenant only, including the app-wide records those lines name, directly or through other app-wide lines, that no earlier import holds (otherwise `broken-reference`), but without the record lines an earlier import already put into that app-tenant and the app-wide records any earlier import already imported (they are `already-imported`; lines naming a record left out still resolve while it is live, and a deleted one is `broken-reference`, see the guide's [`broken-reference` rule](https://multipaas.com/docs/guides/import-existing-data/)), and without each membership line an earlier import into that app-tenant already held (its files list them), unless the user asks to grant it again (a repeat of a membership is not reported; it is skipped only while the earlier invitation for that app-tenant, role and account email is neither accepted nor deleted, a lapsed one included (a repeat creates no invitation of its own, so renew a lapsed one with `multipaas import reissue --app APP_ID --env ENV_ID --import EARLIER_IMPORT_ID` on the earlier import whose run made it; use `--tenant APP_TENANT_ID` in place of `--import` only when `--import` cannot reach it, because `--tenant` re-issues every lapsed import invitation of that app-tenant, other imports' included, and so can renew a role you did not mean to: list them first with `multipaas import invitations --app APP_ID --env ENV_ID --tenant APP_TENANT_ID` and check the list; `import reissue` renews what is lapsed when it runs), and otherwise it creates a new invitation, which re-adds or re-invites the person at their next sign-in); and the users it needs: a user with an unchanged email is linked; a user whose email changed is `already-imported`, so leave it out, and the lines that name it still resolve to the account it was linked to; a user whose account was deleted is `already-imported` too, and leaving it out makes every membership line and `createdBy` that names it `unknown-user`, so drop that user's membership lines and drop the `createdBy` (leave it out or write `null`), so whoever runs the import creates the record. A record line is `already-imported` only when an earlier import put that old id into the same app-tenant, or app-wide for an app-wide line, and a reference finds a record of the line's own app-tenant first, then an app-wide one, never another app-tenant's. Once an old tenant id is in more than one live app-tenant, a line that names it gets `ambiguous-tenant` unless this import's `tenants.jsonl` has a row for it (a row with `name` is `already-imported`, so only an `appTenant` row works): add `{"id": "OLD_TENANT_ID", "appTenant": "APP_TENANT_ID"}` naming the app-tenant this import is for.
- An `id` is at most 1,024 bytes in UTF-8 and has no control characters or unpaired surrogates (problems
  `id-too-long` and `id-invalid-character`).
- A tenant's `name`, a user's `name` and `email`, and every text field value have no NUL character and no unpaired
  surrogate (problem `invalid-character`). Other control characters, such as newlines and tabs, are fine in them.
- A record's `fields`, as UTF-8 JSON, come to under about 3.75 MiB, less when the text holds many quotes or
  backslashes (problem `row-too-large`).
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
are not emailed. They join their tenants at their next sign-in within 7 days of the run. After that they lapse:
`import status` shows the pending, accepted and lapsed counts, and `import invitations` lists the
lapsed ones. `import reissue` gives every membership it re-issues another 7 days from now, with no email. With `--import`
alone, `import reissue` skips a membership whose tenant or role is deleted, or whose role you do not hold, and it stays
lapsed. With `--tenant`, `import reissue` refuses a tenant that is deleted, unknown or outside the environment as not
found, and skips a membership whose role is deleted or not held. Each command takes `--import`, `--tenant`, or both, and
needs at least one. `--tenant` reaches the memberships of imports run before imports recorded their invitations, and,
for `import reissue`, those of a deleted import, which `import reissue --import` refuses as not found.

`import delete` marks an import deleted, and is refused with `DATA_IMPORT_STATE` while the import is checking or
importing. Nothing is removed: its files stay, and so does any data it imported. A deleted import can't be
changed, and `import status` finds it only with `--include-deleted`.

With `--json`, `import create`, `check`, `run`, `status` and `delete` print `{"import": {...}}`, and `import add` prints
`{"importFile": {...}}`. An import holds `id`, `status`, `problemCount`, `problems` (each with `file`, `line`,
`sourceId`, `field`, `code` and `message`), `counts` (each a `key` and a `count`), `files`, `startedAt`,
`finishedAt`, `deletedAt` (set once the import is deleted) and `invitations` (`pending`, `accepted` and `lapsed` counts). `add --kind records` needs `--entity`, the entity's id from `model show`.
`import invitations` prints `{"invitations": [...]}` and `import reissue` prints `{"reissued": n, "skipped": m}`.

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

## API

`api` sends one GraphQL document to the MultiPaaS API as you and prints the server's response, `data` and `errors`
together, as JSON. It is for anything the other commands don't cover. Give the document with `--query`, or from a
file with `--query-file F`, or from standard input with `--query-file -`. `--variables` takes the document's
variables as a JSON object, and `--scope CLIENT[/APP[/APP_TENANT]]` sends the request in that scope; without it the
request has none.

```bash
multipaas api --query '{ me { email } }'
multipaas api --query-file report.graphql --variables '{"id": "<id>"}'
```

It exits 0 when the response is a successful GraphQL response with no `errors`, 1 when it has any (its `data` may
still hold what succeeded) or when the server answered with an error status or a JSON body that isn't a GraphQL
response (the body is still printed), 2 when you aren't signed in, and 3 when the server says to upgrade. A body that
isn't JSON is reported as a `CLI_ERROR` with exit 1, like any other failure. It runs with your own permissions, and the
server refuses anything they don't allow.

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
`schema-update` workflow, which runs when a webapp-api deploy announces itself, nightly, and when run by hand from
the Actions tab, regenerates both from the
live webapp-api schema at the repository secret `SCHEMA_ENDPOINT`, with `scripts/regenerate-schema`. When anything changed, it opens or updates one PR,
`fix: update the webapp-api schema`, authored by the `multipaas-bot` GitHub App. If `genqlient` fails against
the new schema, that PR carries only `schema.graphql`, so its CI fails. The App needs these repository permissions
on this repository: Contents, Pull requests and Workflows, each read and write, and Metadata, read. A deploy of the webapp-api at `SCHEMA_ENDPOINT` announces itself with a `webapp-api-schema-deployed`
`repository_dispatch` event to every repository in this organization that has the `webapp-api-schema-subscriber` topic
and the App installed, as this one does. To regenerate by hand:

```bash
scripts/fetch-schema <your MultiPaaS's webapp-api /graphql> > schema.graphql
go tool genqlient
```

A plain `go build` reports version `dev`, which MultiPaaS refuses. To try a build, stamp a version: `go build -ldflags "-X main.version=0.1.0-rc.1" -o multipaas .`

Releases are cut by release-please. Merging a `feat:` or `fix:` PR to `main` opens or updates a
release PR with the changelog, and merging that PR tags `vX.Y.Z`, which publishes the binaries and the
Homebrew formula.
