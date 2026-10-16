# Flyway Manager

Native Go server with an embedded React console and SQLite. Phase 2 implements local/LDAP/OIDC identity, scoped access, service tokens, the Oracle target/schema registry and encrypted credentials. Migration execution, bundle uploads, run history and Oracle connection probes remain unavailable until Phase 3. No migration results are fabricated.

Requirements: Go 1.26.9+, Node.js 22.12+ and npm for building. Make pins Go 1.26.9. The compiled binary needs no Go, Node, Docker, Java, Oracle client or Flyway installation for Phase 2. Verification targets Linux amd64.

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

Admin manages identities, grants, registry, providers and credentials. Operator and Viewer see granted targets/schemas; Viewer cannot operate. Empty grants deny access. Target grants cover all schemas; schema grants cover one stream. **All operational access, including Admin, requires a scoped grant; PROD additionally requires an explicit PROD grant.** Registry administration and metadata visibility for Admin are global. No operational migration endpoint is enabled in this phase. Oracle execution accounts are separate from application identities, and schema settings do not restrict Oracle privileges.

Service accounts have an active application-user owner and explicit grants. Tokens authenticate using `Authorization: Bearer <token>`, support expiry/revoke/atomic rotation, and never create web sessions or gain administrative API access. Only SHA-256 token hashes and prefixes persist; plaintext is returned once. Disabling the service account or owner, or transferring ownership, permanently revokes its existing tokens. Do not combine a bearer token with a browser session cookie.

Configure LDAP and OIDC in Authentication Settings. Local login remains usable without either integration. LDAP uses verified LDAPS or StartTLS and direct user bind, then reads the configured immutable subject attribute. No LDAP passwords are stored. For AD, configure the appropriate username attribute (often `sAMAccountName`) and immutable subject (for example `objectGUID`); binary subjects use unpadded URL-safe base64. The provider identifier is the exact LDAP URL. OIDC uses verified HTTPS discovery/JWKS, Authorization Code + PKCE, single-use browser-bound state, nonce, issuer/audience/signature/lifetime checks, and issuer + subject mapping. HTTPS redirects cannot downgrade transport. The callback is `/api/v1/auth/oidc/callback` and must match the configured public URL when one is set.

External users must be registered explicitly by an Admin with provider, issuer and immutable subject before login. There is no automatic provisioning, role inference from email, Admin assignment, or fallback to a local user with the same username. Provider configuration is persisted encrypted; saved OIDC client secrets are never returned. Empty client-secret updates preserve the existing value. Saving settings validates configuration; it does not claim live directory/IdP connectivity.

## Registry and encrypted storage

Each database target records host, port, Oracle service/PDB, environment and transport metadata. Each target + schema has independent history owner/table configuration. Duplicate connection identities, schema names and physical history locations are rejected. An execution account can map to multiple schemas; a default account must be active, belong to the same target and have an explicit mapping. Clear default references before removing mappings or disabling that account. Resources deactivate instead of being destructively deleted. Registry settings do not create or modify Oracle databases/users. TCP/TCPS is stored intent; actual Oracle connectivity remains unimplemented.

`<data_dir>/manager.db` and its sidecars remain in the existing Phase 1 location. Data/secret directories use 0700 and files 0600 on Unix; symlinked data/SQLite/key paths are rejected. Versioned, checksummed migration `002_identity_registry.sql` adds Phase 2 tables without changing migration 001 or replacing installation metadata. SQLite remains WAL with full synchronous commits and one connection. Complete migration history and the independent `user_version` checkpoint are verified before pending SQL and ledger changes commit atomically. Old two-column Phase 1 ledgers still upgrade using the pinned original checksum. Never edit applied SQL or ledger rows to silence a startup error.

AES-256-GCM credentials use fresh nonces and record-bound authenticated data. The raw 32-byte master key is stored **outside SQLite** at `<data_dir>/secrets/master.key`, with private permissions. Oracle credentials have write-only creation/replacement/revocation interfaces and metadata-only reads. Startup verifies the installation key check and every saved credential; missing/wrong keys or tampering fail before HTTP starts. **Never delete or regenerate an existing installation's key.** Restore its original key. A small internal secret-store interface allows a future Vault adapter; no Vault integration is advertised or required.

Back up SQLite and the matching key separately, under appropriate access controls. Use [SQLite's online backup API](https://sqlite.org/backup.html), or stop cleanly before copying `manager.db`; a live DB-only copy may omit committed WAL data. Never manually remove WAL/SHM files after a crash. Keep SQLite on local storage, not NFS/SMB. Restore only with all processes stopped, preserve originals, and verify integrity/installation metadata and decryption. Automated backup/restore tooling, lost-key recovery, physical failure drills and OS keychain integration are outside this phase.

## API and verification

[OpenAPI 3.1](docs/openapi.yaml) documents implemented routes under `/api/v1`: authentication/sessions, users/grants, service accounts/tokens, provider settings, database targets/schemas, execution accounts/credentials and security audit. JSON uses `{data: ...}` or `{error: {code, message, field?}}`; mutation bodies are limited to 1 MiB, unknown fields rejected. Lists support bounded limit/offset and relevant filters. Sensitive routes always authenticate on the server; management requires Admin. Health/setup metadata are public. Unfinished execution/artifact routes authenticate, then return typed HTTP 501.

Natural uniqueness prevents duplicate registrations and active token names. Concurrent setup is single-use. Token rotation atomically revokes the old token and rejects repeat rotation with 409; lost one-time plaintext must be revoked/reissued. Credential replacement is a repeatable write, recorded each time. Phase 3 submission idempotency is not implemented because no jobs can be submitted.

Audit records identify the authenticated actor, action, target, UTC timestamp, outcome and server-generated request ID. Successful business mutations and audit records commit in the same SQLite transaction. Authentication events and rejected sensitive requests are durable too. The API exposes no audit update/delete operation. Logs contain no request bodies, queries, passwords, tokens or decrypted credentials. Audit is durable application history, not protection against an OS/SQLite administrator rewriting the database.

```sh
make check           # build, vet, Go tests incl native restart, lint, frontend unit tests
make test-e2e        # Chromium E2E against the embedded binary and isolated temporary data
make test-race       # Go race tests
make security        # npm audit and Go vulnerability checks
```

Install Chromium once with `cd web && npx playwright install chromium`, or set `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`. Browser artifacts are ignored under `web/test-results/`. All tests use temporary directories; no existing installation or external infrastructure is modified. [Phase 2 verification and limitations](docs/phase2-verification.md) records the evidence and remaining acceptance gates. No live AD, Entra ID, Keycloak or Oracle verification is claimed.

The [canonical product specification](flyway-manager-product-spec.md) remains unchanged. Existing design tokens, shared components and navigation follow [the approved handoff](docs/design/design-system.md). Implemented business pages use real APIs with loading, empty, error and denied states. Fixture authentication and migration results have been removed. The Stitch project was not modified.
