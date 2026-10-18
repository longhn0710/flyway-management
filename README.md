# Flyway Manager

Native Go server with an embedded React console and SQLite. Phase 3 adds immutable SQL bundles, integrity-pinned Flyway CLI execution, durable asynchronous jobs, stream history, logs and guarded automatic repair to the existing identity, RBAC and encrypted registry. **IMPLEMENTED — Oracle Free application smoke verified; Oracle 19c/21c remain unverified.** No Oracle 19c/21c compatibility or production-readiness claim is made from fixture tests.

PostgreSQL 17 connection/discovery, native Info and qualified transactional migration are available with password/SCRAM and verify-full TLS. The [Manager migration and recovery acceptance](docs/postgresql-migration-acceptance.md) records Gates E/F, conservative holds and profile limits. The [foundation acceptance](docs/postgresql-foundation-acceptance.md) retains SQLite v8 preservation and the original cancellation failure. Oracle retains its operational migration policy.

Requirements: Go 1.26.9+, Node.js 22.12+ and npm for building. Make pins Go 1.26.9. The compiled console needs no Go, Node or Docker. Migration execution additionally needs an approved Flyway distribution, compatible Java and Oracle JDBC/module assets. Verification targets Linux amd64; other operating systems require acceptance.

```sh
make install
make build
make run
# Open http://127.0.0.1:8080
```

On first launch, read `<data_dir>/secrets/bootstrap.token` on the server and enter it in the initial administrator form. The startup log and setup screen show the **path**, never the token. For the default installation:

```sh
cat ~/.flyway-manager/secrets/bootstrap.token
```

Create a local Admin with a password of at least 12 bytes. Setup is transactional, protected against concurrent/repeated initialization, and stays closed after restart. The setup token grants no access after setup completes. No default accounts or fixture business records are inserted. Keep a working local administrator for recovery; the API preserves the final active local Admin and prevents removal of the final administrator whose provider is enabled.

The compiled `bin/flyway-manager` contains the React application, local fonts/icons and SQLite driver. `web/dist` is generated and ignored by Git; build before running Go tests on a fresh checkout. For development, build once and use two terminals:

```sh
make dev-api  # 127.0.0.1:8080
make dev-web  # 127.0.0.1:5173, /api proxied to Go
```

Configuration loads defaults, then an optional JSON file selected by `FLYWAY_MANAGER_CONFIG`, then environment overrides. See [config.example.json](config.example.json).

| Variable | Default | Purpose |
| --- | --- | --- |
| `FLYWAY_MANAGER_CONFIG` | unset | JSON configuration path |
| `FLYWAY_MANAGER_LISTEN_ADDRESS` | `127.0.0.1:8080` | Native HTTP listener |
| `FLYWAY_MANAGER_DATA_DIR` | `~/.flyway-manager` | Existing persistent SQLite directory |
| `FLYWAY_MANAGER_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `FLYWAY_MANAGER_PUBLIC_URL` | unset | Browser origin, e.g. `https://migrations.example`; HTTP allowed only for loopback |

```sh
FLYWAY_MANAGER_DATA_DIR=./.data FLYWAY_MANAGER_LISTEN_ADDRESS=127.0.0.1:8081 make run
```

The default listener is loopback. Remote use requires an explicitly configured HTTPS public URL and trusted TLS reverse proxy, with direct backend access denied. The application ignores forwarded identity/IP/protocol headers. With a configured public URL, browser Origin must match it, and session cookies are Secure for HTTPS. Configure HSTS and remote network policy at the proxy. Vite development uses its same-origin proxy; leave `public_url` unset or set the Vite origin for that development process.

## Identity and permissions

Browser login uses an opaque HttpOnly, SameSite=Lax session cookie. Only a hash of the session secret is stored. Sessions expire after 12 hours absolute or 30 minutes idle; logout, password changes, disabling users and role changes revoke sessions. The profile menu supports password changes and viewing/revoking own sessions. Browser state is never an authentication authority.

Cookie-authenticated mutations require both a matching Origin and `X-CSRF-Token` returned by login/bootstrap or `GET /api/v1/auth/me`. Initial setup and password-provider login require matching Origin too. Cookies are Secure on direct TLS or when the configured public URL uses HTTPS. Local passwords use Argon2id (64 MiB, three iterations, two lanes, random 16-byte salt). Authentication attempts are limited by direct client address and username, without account-existence errors. Limits are in memory and reset on restart; do not treat them as a distributed abuse-control service.

Admin manages identities, grants, registry, providers and credentials. Operator and Viewer see granted targets/schemas; Viewer cannot operate. Empty grants deny access. Target grants cover all schemas; schema grants cover one stream. **All operational access, including Admin, requires a scoped grant; PROD additionally requires an explicit PROD grant.** Registry administration and metadata visibility for Admin are global. Oracle execution accounts are separate from application identities, and schema settings do not restrict Oracle privileges.

Service accounts have an active application-user owner and explicit grants. Tokens authenticate using `Authorization: Bearer <token>`, support expiry/revoke/atomic rotation, and never create web sessions or gain administrative API access. Only SHA-256 token hashes and prefixes persist; plaintext is returned once. Disabling the service account or owner, or transferring ownership, permanently revokes its existing tokens. Do not combine a bearer token with a browser session cookie.

Configure LDAP and OIDC in Authentication Settings. Local login remains usable without either integration. LDAP uses verified LDAPS or StartTLS and direct user bind, then reads the configured immutable subject attribute. No LDAP passwords are stored. For AD, configure the appropriate username attribute (often `sAMAccountName`) and immutable subject (for example `objectGUID`); binary subjects use unpadded URL-safe base64. The provider identifier is the exact LDAP URL. OIDC uses verified HTTPS discovery/JWKS, Authorization Code + PKCE, single-use browser-bound state, nonce, issuer/audience/signature/lifetime checks, and issuer + subject mapping. HTTPS redirects cannot downgrade transport. The callback is `/api/v1/auth/oidc/callback` and must match the configured public URL when one is set.

External users must be registered explicitly by an Admin with provider, issuer and immutable subject before login. There is no automatic provisioning, role inference from email, Admin assignment, or fallback to a local user with the same username. Provider configuration is persisted encrypted; saved OIDC client secrets are never returned. Empty client-secret updates preserve the existing value. Saving settings validates configuration; it does not claim live directory/IdP connectivity.

## Instance-centric console

The reviewed Stitch console groups the operational journey under **Home → Instances → Connections → Databases / Schemas → Run Migration → Migration History**. Users & Permissions, Service Accounts and Authentication Settings remain available to administrators. Existing `/databases` links redirect into instance context; `/execution-accounts` redirects to Instances, where accounts are managed per connection. Oracle is operational. PostgreSQL actions follow the server capability policy; MySQL and SQL Server remain unavailable. Registry activity is not a live connection-health claim.

See [implementation review and evidence](docs/stitch-implementation-review.md) for the current screenshots, verification results and API capability limits. This frontend change does not authorize merge or deployment.

## Oracle connection wizard

The probe uses the approved Flyway distribution’s pinned Java/JDBC assets and requires that runtime to be configured. It executes a fixed set of metadata SELECTs and never calls the Flyway engine. Credentials go to the private helper through stdin, with no credential arguments or environment variables.

Use **Instances → Add instance** to enter the address, service/PDB and Oracle username/password in one form. **Test Connection** authenticates and automatically discovers visible schemas without persisting credentials or invoking Flyway/DDL. Discovery reads all accessible `ALL_USERS`, `ALL_OBJECTS`, `SESSION_PRIVS`, `SESSION_ROLES` and supported schema-privilege metadata, without a row cap. The UI shows the complete returned count, filtering, per-schema evidence and any metadata restrictions. A 5–30 second overall budget, bounded individual reads and a 16 MiB metadata transport safety limit preserve partial results with explicit warnings when metadata is unavailable, times out or exceeds that byte budget. There is no schema-row limit. TCPS uses trusted TLS and server identity verification; it has no insecure certificate bypass.

The next step explicitly selects allowed schemas. Only an appropriate login schema defaults selected. Oracle-maintained/unsupported schemas are rejected; unknown capability requires acknowledgement. Metadata never labels arbitrary migration DDL as verified. The allowlist adds an application restriction to native Oracle privileges; it grants no Oracle rights and does not constrain cross-schema references inside SQL.

After **Review selection**, **Save instance** repeats discovery and commits the target, account, encrypted credential, schema records and mappings in one SQLite transaction. Optional explicit current-user grants are scoped to selected schemas, with a separate PROD authorization checkbox. Otherwise existing scoped RBAC remains required. Open an instance’s **Connections** tab to test an account, edit its allowed schemas or rotate its credential; select the account being edited to preserve other execution accounts. Empty password edits reuse the encrypted saved credential. Each account has its own allowlist. Removing a schema blocks future preflight/submission/dispatch with that account and removes it from upload choices when no other authorized account permits it. This retains history and does not delete Oracle or audit records. Existing advanced configurations retain their original behavior until explicitly edited through the wizard. Targets with manager migration history or other execution accounts cannot change destination or environment through the wizard.

The normal migration picker uses `/migration-connections` and `/migration-connections/{id}/schemas`, which apply current operational RBAC, PROD rules, activity, mapped credentials and the connection allowlist before returning choices. Server-side execution rechecks the allowlist and account mapping immediately before SQL. Historical records remain readable under the existing history RBAC; completed idempotent submissions still resolve to their original run.

## Registry and encrypted storage

Each database target records host, port, Oracle service/PDB, environment and transport metadata. Each target + schema has independent history owner/table configuration. Duplicate connection identities, schema names and physical history locations are rejected. An execution account can map to multiple schemas; a default account must be active, belong to the same target and have an explicit mapping. Clear default references before removing mappings or disabling that account. Resources deactivate instead of being destructively deleted. Registry settings do not create or modify Oracle databases/users. The preflight endpoint contacts the configured service through Flyway info/validate when a verified runtime is available. It reports network/authentication/service/history evidence separately from unverified DBID identity and DDL privileges. A saved TCP/TCPS setting alone is not a connection test.

`<data_dir>/manager.db` and its sidecars remain in the existing Phase 1 location. Data/secret directories use 0700 and files 0600 on Unix; symlinked data/SQLite/key paths are rejected. Versioned migrations `002_identity_registry.sql`, `003_execution.sql` and `004_connection_wizard.sql` add identity/registry, execution and connection allowlist tables without modifying migration 001/002 or replacing installation metadata. SQLite remains WAL with full synchronous commits and one connection. Complete migration history and the independent `user_version` checkpoint are verified before pending SQL and ledger changes commit atomically. Old two-column Phase 1 ledgers still upgrade using the pinned original checksum. Never edit applied SQL or ledger rows to silence a startup error.

AES-256-GCM credentials use fresh nonces and record-bound authenticated data. The raw 32-byte master key is stored **outside SQLite** at `<data_dir>/secrets/master.key`, with private permissions. Oracle credentials have write-only creation/replacement/revocation interfaces and metadata-only reads. Startup verifies the installation key check and every saved credential; missing/wrong keys or tampering fail before HTTP starts. **Never delete or regenerate an existing installation's key.** Restore its original key. A small internal secret-store interface allows a future Vault adapter; no Vault integration is advertised or required.

Back up SQLite and the matching key separately, under appropriate access controls. Use [SQLite's online backup API](https://sqlite.org/backup.html), or stop cleanly before copying `manager.db`; a live DB-only copy may omit committed WAL data. Never manually remove WAL/SHM files after a crash. Keep SQLite on local storage, not NFS/SMB. Restore only with all processes stopped, preserve originals, and verify integrity/installation metadata and decryption. Automated backup/restore tooling, lost-key recovery, physical failure drills and OS keychain integration are outside this phase.

## API and verification

[OpenAPI 3.1](docs/openapi.yaml) documents implemented routes under `/api/v1`: authentication/sessions, users/grants, service accounts/tokens, provider settings, database targets/schemas, execution accounts/credentials and security audit. JSON uses `{data: ...}` or `{error: {code, message, field?}}`; JSON mutation bodies are limited to 1 MiB, unknown fields rejected. Artifact uploads use a separate bounded multipart stream. Lists support bounded limit/offset and relevant filters. Sensitive routes always authenticate on the server; management requires Admin. Health/setup metadata are public. Runtime, artifact, preflight and migration-run routes enforce authentication plus target/schema-scoped authorization.

Natural uniqueness prevents duplicate registrations and active token names. Concurrent setup is single-use. Token rotation atomically revokes the old token and rejects repeat rotation with 409; lost one-time plaintext must be revoked/reissued. Credential replacement is a repeatable write, recorded each time. Migration submissions require `Idempotency-Key`. The same authenticated principal, key and canonical request return the original Run ID; conflicting reuse returns 409. Records persist for the lifetime of the installation; no automatic retention/pruning runs.

Audit records identify the authenticated actor, action, target, UTC timestamp, outcome and server-generated request ID. Successful business mutations and audit records commit in the same SQLite transaction. Authentication events and rejected sensitive requests are durable too. The API exposes no audit update/delete operation. Logs contain no request bodies, queries, passwords, tokens or decrypted credentials. Audit is durable application history, not protection against an OS/SQLite administrator rewriting the database.

```sh
make check           # build, vet, Go tests incl native restart, lint, frontend unit tests
make test-e2e        # Chromium E2E against the embedded binary and isolated temporary data
make test-race       # Go race tests
make security        # npm audit and Go vulnerability checks
```

Install Chromium once with `cd web && npx playwright install chromium`, or set `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`. Browser artifacts are ignored under `web/test-results/`. The standard checks use temporary fixtures and do not modify an existing installation or external infrastructure. [Phase 3 verification](docs/phase3-verification.md) records implementation evidence and open acceptance gates; the [Phase 2 report](docs/phase2-verification.md) remains the historical baseline. Separately authorized [Oracle Free application acceptance](docs/oracle-free-lab-acceptance.md) verifies real preflight and a V2 manager job against the isolated disposable lab. Oracle 19c/21c and live AD, Entra ID and Keycloak acceptance remain open.

The [canonical product specification](flyway-manager-product-spec.md) remains unchanged. Existing design tokens, shared components and navigation follow [the approved handoff](docs/design/design-system.md). Implemented business pages use real APIs with loading, empty, error and denied states. Fixture authentication and migration results have been removed. The Stitch project was not modified.


## Flyway runtime and migration execution

Flyway remains the SQL engine. Obtain an authorized distribution and a trusted, approved complete-file manifest; verify Flyway/JRE/JDBC acquisition and redistribution terms separately. The application does not silently download, upgrade, or repackage third-party binaries. A locally generated checksum alone does not establish provenance.

The distribution root (the directory containing the executable) must contain `flyway-manifest.sha256`, listing every other regular file as `<SHA-256><two spaces><relative path>`. Pin the SHA-256 of that manifest's exact bytes in `flyway.sha256`. Unlisted/missing/modified files and symlinks fail readiness. Required Oracle JDBC and Flyway Oracle module JARs must be included. A manifest-covered bundled `jre/bin/java` is preferred; otherwise the restricted system Java path must be available. `GET /api/v1/runtime` reports local integrity/version/dependency readiness, independently of Oracle connectivity.

For an operator-approved source directory, optional local installation copies, validates and atomically publishes private files without replacing an existing version:

```sh
./bin/flyway-manager runtime-install --source /approved/flyway-distribution \
  --executable flyway --version APPROVED_VERSION --sha256 APPROVED_MANIFEST_SHA256
```

Copy the emitted `flyway` configuration into the server JSON configuration selected by `FLYWAY_MANAGER_CONFIG`, or configure an approved existing executable directly. `config.example.json` leaves executable/version/pin empty, so fresh installations cannot execute migrations. Runtime settings are local administrator configuration; there is no browser binary-install endpoint. Existing streams pin the version, manifest digest and connection/history configuration; changed pins never take over an existing stream automatically. Every Flyway command revalidates distribution integrity and exact version.

Use Run Migration to select one granted target/schema and an active mapped account with a configured credential, upload files or a folder/archive, inspect the immutable manifest, run preflight, then confirm the exact destination. The default history schema is the target schema. Dedicated history schemas deliberately block execution until Oracle CURRENT_SCHEMA/cross-schema behavior is accepted in a disposable lab. Read-only diagnostics do not prove every migration privilege or constrain cross-schema SQL.

Artifact input and normalized SQL bytes, manifests, server SHA-256 checksums, frozen run files, logs, and repair evidence stay in SQLite BLOB/relational storage. No canonical artifact bytes are moved to a runtime directory. Defaults: 50 MiB uploaded bytes (plus 64 KiB multipart overhead), 200 MiB expanded bytes, 20 MiB per SQL file, 1,000 files/archive entries, path depth 16, path length 1,024 bytes and expansion ratio 200. `artifact_limits` configures these within enforced safety ceilings. Complete stream locations use the same expanded-size/file-count ceiling. ZIP/TAR links, special files, traversal, nested archives, duplicate versions/repeatables and unsupported input fail closed. SQL bytes are preserved exactly; versioned and repeatable filenames must use `V...__description.sql` / `R__description.sql`. No user config, callback, manifest or SQL rewrite is accepted.

For API clients, upload multipart `files` with `target_id` and `schema_id` query parameters, then POST `/api/v1/migration-preflight` and `/api/v1/migration-runs` with target/schema/account/artifact IDs. Folder paths belong in each part's filename. Browser mutation requests require Origin plus CSRF; bearer requests remain scoped service identities. Submission returns 202 with `run_id`, `status_url`, `logs_url` and the durable run snapshot. Poll the status URL; run records and searchable/paginated logs are available through the API and Run Details.

The SQLite lifecycle is `QUEUED → PREFLIGHT → RUNNING → SUCCEEDED`. Eligible Oracle failures can enter guarded `REPAIRING → FAILED`; PostgreSQL failed or ambiguous migrations become held `NEEDS_REVIEW` with Repair skipped. Additional states are `BLOCKED`, `NEEDS_REVIEW` and queued-only `CANCELLED`. Four worker slots permit independent streams; SQLite claims, a process ownership lock and stream mutexes serialize each target/schema through repair. These application locks do not provide distributed database locking. External Flyway/DBA writers must be operationally excluded from a managed stream during execution; cross-schema SQL can still interfere with other streams.

Historical files are reconstructed for successive uploads and versioned content drift is rejected. Frozen jobs that depend on a failed/cancelled predecessor block. Historical versioned SQL that Flyway reports Pending must be explicitly present in the new upload; a later Vn never implicitly retries failed SQL. Queued runs recheck identity/token/session, grants, activity, mapping and the frozen credential/configuration version before dispatch.

Oracle automatic Repair requires one newly failed, originally pending versioned migration, unchanged unrelated applied history, and Flyway validation reporting only the corresponding failed-version error. The same frozen locations and configuration are used once. Repair must report exactly one removal, no alignment/deletion, successful validation, and the complete original resolver view restored (including the failed version as Pending). Otherwise the run/stream needs review. Repair never reruns SQL, rolls back DDL, or converts migration FAILED into SUCCEEDED. The conservative guard skips failures that also applied additional migrations, any repeatable/superseded history (its repair effects require separate acceptance), and uncertain/malformed evidence.

A restart recovers queued work but holds interrupted/claimed work for review. Read-only info/validate reconciliation records evidence where current permissions, unchanged snapshots and the runtime permit it; it never retries SQL, repairs on restart, clears an uncertain hold, or manufactures a successful exit. Reconciliation/partial-DDL recovery remains an operator/DBA acceptance boundary, not an approval or rollback workflow. Keep the original run/artifact evidence and reconcile the database before resuming a held stream. No generic hold-clear, arbitrary SQL, manual repair or retry endpoint is provided.

Flyway processes receive explicit argv, a private config file, restricted environment, private ephemeral workspaces, a 512 MiB Java heap cap, at most four concurrent runtime checks/processes shared across HTTP/jobs, and bounded output. Defaults allow 300 seconds per worker command (configurable up to 3,600); synchronous HTTP preflight/readiness has a separate 50-second overall budget and may honestly return NOT_VERIFIED on a slow environment. Unix cancellation kills the child process group. Logs cap each stdout/stderr at 2 MiB and durable run logs at 50 MiB / 10,000 process-output entries. Snapshots/evidence remain after shutdown; temporary workspace cleanup is best effort on ordinary exit. Crash leftovers are private and require operational housekeeping after confirming no process uses them.

Run records include the exact nonsecret Flyway option snapshot; a later application policy change blocks queued jobs/streams instead of silently changing execution semantics. CLI output is persisted on command completion. An abruptly killed manager can lose buffered in-flight output; the durable run state remains uncertain and startup attempts independent read-only history reconciliation. PostgreSQL imports durable lifecycle journals before database inspection and never releases a hold or replays SQL during reconciliation.

## Homelab deployment

The optional non-production Kubernetes packaging uses one nonroot Manager with
persistent SQLite and a separately mounted deployment key. See
[homelab operations](docs/homelab-operations.md) for the GitOps chart location,
HTTPS, status, restart, backup/isolated restore, and image upgrade commands.
`FLYWAY_MANAGER_MASTER_KEY_FILE` (or `master_key_file`) selects an existing private
32-byte key independently of the data directory. An absent/wrong external key
fails closed; no replacement is generated. Native installations retain their
existing local key behavior. `flyway-manager backup --output /private/new.db`
creates a consistent SQLite snapshot; back up the encryption key separately.

The Docker image contains the approved complete Flyway 13.10.3 runtime and needs
no runtime download. CI verifies the application before publishing a versioned
GHCR image; GitOps pins its digest. See [external browser acceptance](deploy/acceptance/README.md)
for the HTTPS browser harness, which never submits migrations.

Phase 4 deployment results and immutable revisions: [homelab acceptance](docs/phase4-homelab-acceptance.md).

### Projects and native Flyway Info

Use **Projects** to group existing schema or instance references across environments. Administrators manage membership and archive/restore projects; assignment grants no access. Project history uses current membership and existing read permissions, while each run keeps its immutable original destination and actor snapshot.

Administrators can delete an empty project by typing its exact current name. Projects with any membership are blocked, including archived or inactive-resource memberships. Deletion removes project metadata and records an audit event; database resources, migration history and prior audits are retained. See the [Phase 9 plan](docs/phase9-enterprise-integrations.md) for integration scope and release boundaries.

Open **Flyway Info** from a schema, project resource or migration run to inspect the native migration table independently of execution logs. Recorded views show source, artifact and capture time. Authorized operators can select a mapped execution account and immutable artifact and run a bounded, read-only native `info` refresh. Migration/repair commands and stream safety checks remain unchanged. Pending/Missing and other resolver states depend on the captured migration files; the interface does not claim external artifact completeness.

See [operator acceptance and verification](docs/projects-flyway-info-acceptance.md) for behavior, safety boundaries, evidence and rollout limits. These features introduce SQLite migrations 005/006/007 (schema version 7); preserve a consistent registry backup and its original encryption key before a separately authorized rollout.
