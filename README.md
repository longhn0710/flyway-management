# Flyway Manager

Native Go server with an embedded React console and SQLite. Phase 1 implements the application foundation and the approved Stitch UI; it does **not** execute migrations or authenticate users yet.

Requirements: Go 1.26.9+, Node.js 22.12+ and npm. Make pins the patched Go 1.26.9 toolchain (downloaded automatically if needed). No Docker, Java, Oracle or Flyway installation is needed for this phase. Verification was performed on Linux amd64.

```sh
make install
make build
make run
# Open http://127.0.0.1:8080 and choose “Mở console minh họa”.
```

The resulting `bin/flyway-manager` includes the React application, fonts, icons and SQLite driver. Copying this binary to a compatible machine is sufficient to run Phase 1; Node and Go are build-time tools only. The browser makes no requests to external font or CDN services.

For development, build once, then use two terminals:

```sh
make dev-api  # Go API on 127.0.0.1:8080
make dev-web  # Vite on 127.0.0.1:5173, /api proxied to Go
```

Use Vite during frontend development; rebuild the binary to update embedded production assets. `web/dist` is generated and intentionally ignored by Git, so a fresh checkout must build the frontend before `go test` or `go run`.

Configuration uses defaults, an optional JSON file selected with `FLYWAY_MANAGER_CONFIG`, then environment overrides:

| Variable | Default | Purpose |
| --- | --- | --- |
| `FLYWAY_MANAGER_CONFIG` | unset | Path to JSON configuration; see [config.example.json](config.example.json) |
| `FLYWAY_MANAGER_LISTEN_ADDRESS` | `127.0.0.1:8080` | Native HTTP listener |
| `FLYWAY_MANAGER_DATA_DIR` | `~/.flyway-manager` | Persistent directory containing `manager.db` and SQLite WAL files |
| `FLYWAY_MANAGER_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

```sh
FLYWAY_MANAGER_CONFIG=./config.example.json make run
# Or an isolated local installation:
FLYWAY_MANAGER_DATA_DIR=./.data FLYWAY_MANAGER_LISTEN_ADDRESS=127.0.0.1:8081 ./bin/flyway-manager
```

The data directory is restricted to mode 0700 and SQLite files to 0600 on Unix. Relative data paths resolve from the process working directory. The final data-directory component and SQLite file paths must not be symlinks; use a real, dedicated directory owned by the service user on a local filesystem. Startup verifies WAL mode and uses full synchronous commits. Installation metadata persists across restarts; no fixture business records are inserted. SIGINT / SIGTERM gracefully stop HTTP before closing SQLite.

Startup checks the complete, contiguous SQL migration sequence and every applied ledger entry's filename, SHA-256 and timestamp, with an independent transactional SQLite `user_version` checkpoint to detect lost trailing history before applying pending migrations. Pending SQL and ledger changes commit together; an error rolls them all back. Existing Phase 1 two-column ledgers upgrade automatically using the pinned checksum of the original `001_initial.sql`, preserving installation data. This establishes a baseline for future drift detection; the old ledger cannot prove which SQL ran historically. Never edit applied SQL, delete ledger rows or replace a missing migration to make startup pass. Keep the original database and diagnostics, restore the matching binary/migration files, or restore a verified backup before investigating a history error.

Phase 1 is an **unauthenticated preview**. Its sessionStorage choice selects demo screens and grants no identity or permissions. Health/system metadata are public, and all business capabilities remain unavailable. Future business routes must enforce server-side authentication and action/target/schema RBAC, independently of frontend navigation, preview state or caller-supplied identity fields. The server ignores forwarded-IP headers; any future trusted-proxy configuration must name and validate trusted proxies explicitly.

The listener defaults to loopback. A configured non-loopback/wildcard listener emits a startup warning; it is still plain HTTP with no login protection. For a colleague on a LAN, prefer keeping the listener on loopback and using an SSH tunnel (`ssh -L 8080:127.0.0.1:8080 user@manager-host`), then open `http://127.0.0.1:8080`. Direct LAN preview requires an explicit address such as `FLYWAY_MANAGER_LISTEN_ADDRESS=192.168.1.20:8080`, a firewall restricted to intended clients and a trusted HTTPS reverse proxy for remote browser traffic. Deny direct access to its HTTP backend. HTTPS alone does not authenticate users: only public installation metadata and illustrative records are available in this phase. Do not expose Phase 1 to the internet or enter real credentials. No HSTS is sent by the plain HTTP server; configure transport policy at the HTTPS proxy. Responses include CSP, frame protection, no-referrer and nosniff headers; inline styles remain allowed for the approved UI.

Use [SQLite's online backup API](https://sqlite.org/backup.html) or stop the process cleanly before copying `manager.db`. A live database copy alone may omit committed data still in [`manager.db-wal`](https://sqlite.org/wal.html); never manually delete WAL/SHM files, including after a crash. Keep SQLite on local storage rather than NFS/SMB. On restart SQLite recovers committed WAL changes and discards incomplete transactions. Before restoring, stop all processes using the directory, preserve the original database and sidecars, restore a consistent snapshot as a unit, and verify `PRAGMA integrity_check` and installation metadata. Automated backup/restore tooling and power-loss/storage-failure drills remain future acceptance work; no production directory was touched by these tests.

The real API surface is `GET /api/v1/health` and `GET /api/v1/system`, returning a `{ "data": ... }` envelope. Health checks SQLite and returns the schema version, stable installation ID and explicit capability flags. Known unfinished business routes return HTTP 501 with `{ "error": { "code": "NOT_IMPLEMENTED", "message": "..." } }`; unknown API routes return JSON 404. A health failure returns 503. No upload, registry, secret, identity or execution mutation is accepted.

The console includes Login, Run History, Run Migration and Review, running/failed Run Details and Logs, Database & Schema, Database Details, Execution Accounts, Users & Access, Service Accounts, and Authentication Settings. It uses the shared 216px sidebar, 56px header, teal palette, Geist typography and JetBrains Mono from [the design handoff](docs/design/design-system.md).

Implemented interactions include URL-backed history filters and pagination, sorting and column selection, schema/account selection, local file metadata and SHA-256 checksums, preserved migration drafts, log search/source/level filters and fixture downloads, target expansion/filtering, credential replacement drafts, scoped permission editors, explicit PROD grants, provider tabs and confirmation dialogs. Dialogs support keyboard focus trapping, Escape and focus restoration. Small screens use a navigation dialog and history cards.

All business data is explicitly labeled **development fixtures** and imported through the named `previewFixtures` export. API clients have no fixture fallback; future business pages must replace this preview source as a whole, never merge or backfill API results with illustrative records. Entering the preview is not authentication; its sessionStorage flag only remembers the UI preview choice. Drafts stay in React memory and reset on reload. File selection does not upload, extract, validate Flyway compatibility or establish continuity. Draft `localChecksum` values are untrusted browser display metadata. Any future upload API must compute its own checksum from received bytes and validate normalized content on the server; a supplied digest is never proof of validation. Credential inputs should contain illustrative values only. Saved passwords are never revealed; token examples are unusable placeholders. Issuance, persistence, connection probes and execution actions stay disabled. Illustrative run timestamps are historical snapshots, not live activity.

```sh
make check          # production build, native build, Go vet/tests, ESLint, frontend unit tests
cd web
npx playwright install chromium  # once, when no browser is installed
cd ..
make test-e2e       # browser tests against the compiled Go binary on port 18081
```

`npm --prefix web run typecheck`, `npm --prefix web run lint`, `npm --prefix web test` and `GOTOOLCHAIN=go1.26.9 go test ./cmd/... ./internal/... ./web` are also available individually. To use an existing Chromium executable, set `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`. Browser reports, traces and screenshots are written under ignored `web/test-results/`. Tests use isolated temporary SQLite directories and never contact Oracle. `npm --prefix web audit` checks JavaScript dependencies.

Repository layout: `cmd/flyway-manager` owns process lifecycle; `internal/config`, `internal/database`, `internal/httpapi` contain the Go foundation; `web/src` contains the React application and typed contracts; `web/embed.go` embeds its generated distribution. SQLite migration files are under `internal/database/migrations`. Fixtures live only in `web/src/lib/fixtures.ts`.

The canonical [product specification](flyway-manager-product-spec.md) remains the functional source of truth. The [Stitch project](https://stitch.withgoogle.com/projects/12852677200871379262) is the visual source. Twelve canonical screens were retrieved read-only via Stitch MCP for this implementation; no Stitch project changes were made. Existing design-review notes describe historical handoff gaps. In application code, the sidebar is kept at the top, timestamps stay at least 12px, CORE_BANKING uses its own history location, token storage is described as hashing, SSE examples use per-run URLs, and LDAP settings use direct user bind without saved LDAP passwords. Unsupported compliance, TLS-version and operational-success claims were removed. Preview notices and disabled actions intentionally add Phase 1 context to the approved layouts.

Original committed Phase 1 verification (before this hardening review): `make check` passed (TypeScript/production build, static native binary build, Go vet/tests, ESLint and 7 frontend unit tests). All 12 Chromium browser tests passed against the embedded Go build, including route rendering at 1440px and 390px without page overflow. A separate native process smoke test confirmed stable installation metadata across two launches, WAL mode, SQLite integrity, private file permissions, embedded JavaScript delivery, HTTP 501 for execution submission and clean SIGTERM shutdown. npm audit and Go vulnerability checking with Go 1.26.9 reported no findings. Representative history, migration and access layouts were manually compared with Stitch screenshots; there is no automated pixel-diff baseline. Other operating systems and real Oracle behavior have not been validated.

Hardening verification: `make check` passed, including the 7 frontend unit tests; all 15 Chromium tests passed against the rebuilt embedded binary. Go race tests passed. Added tests cover edited SQL/names, missing bundled/applied versions, inconsistent ledgers, legacy upgrade/data preservation, transactional failure/restart/retry, relative paths, private WAL files and rejected symlinks. An isolated child process exits without closing SQLite to verify committed WAL recovery, unfinished-write rollback and integrity after restart. Native smoke checks verified stable installation metadata, loopback versus wildcard listener warnings, security headers, rejected forged business submissions and clean SIGTERM shutdown. These checks use temporary directories and do not upgrade an existing user installation. The original SQLite migration file is unchanged.

Next phase: implement real local authentication/bootstrap and target/schema RBAC, the registry, and encrypted write-only credential storage with a master key outside SQLite. Then implement secure bundle normalization and immutable migration continuity before introducing Flyway execution. Oracle/Flyway compatibility, conditional repair safety, distribution licensing, restore drills, remote HTTPS and cross-platform packaging remain acceptance gates in the product specification.
