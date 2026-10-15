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

The data directory is restricted to mode 0700 and the database to 0600 on Unix. Startup applies numbered SQL migrations transactionally. Installation metadata persists across restarts; no fixture business records are inserted. SIGINT / SIGTERM gracefully stop HTTP before closing SQLite. Keep this Phase 1 process on a trusted local interface: authentication and remote HTTPS remain future work.

The real API surface is `GET /api/v1/health` and `GET /api/v1/system`, returning a `{ "data": ... }` envelope. Health checks SQLite and returns the schema version, stable installation ID and explicit capability flags. Known unfinished business routes return HTTP 501 with `{ "error": { "code": "NOT_IMPLEMENTED", "message": "..." } }`; unknown API routes return JSON 404. A health failure returns 503. No upload, registry, secret, identity or execution mutation is accepted.

The console includes Login, Run History, Run Migration and Review, running/failed Run Details and Logs, Database & Schema, Database Details, Execution Accounts, Users & Access, Service Accounts, and Authentication Settings. It uses the shared 216px sidebar, 56px header, teal palette, Geist typography and JetBrains Mono from [the design handoff](docs/design/design-system.md).

Implemented interactions include URL-backed history filters and pagination, sorting and column selection, schema/account selection, local file metadata and SHA-256 checksums, preserved migration drafts, log search/source/level filters and fixture downloads, target expansion/filtering, credential replacement drafts, scoped permission editors, explicit PROD grants, provider tabs and confirmation dialogs. Dialogs support keyboard focus trapping, Escape and focus restoration. Small screens use a navigation dialog and history cards.

All business data is explicitly labeled **development fixtures**. Entering the preview is not authentication; its sessionStorage flag only remembers the UI preview choice. Drafts stay in React memory and reset on reload. File selection does not upload, extract, validate Flyway compatibility or establish continuity. Credential inputs should contain illustrative values only. Saved passwords are never revealed; token examples are unusable placeholders. Issuance, persistence, connection probes and execution actions stay disabled. Illustrative run timestamps are historical snapshots, not live activity.

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

Verification for this Phase 1 delivery: `make check` passed (TypeScript/production build, static native binary build, Go vet/tests, ESLint and 7 frontend unit tests). All 12 Chromium browser tests passed against the embedded Go build, including route rendering at 1440px and 390px without page overflow. A separate native process smoke test confirmed stable installation metadata across two launches, WAL mode, SQLite integrity, private file permissions, embedded JavaScript delivery, HTTP 501 for execution submission and clean SIGTERM shutdown. npm audit and Go vulnerability checking with Go 1.26.9 reported no findings. Representative history, migration and access layouts were manually compared with Stitch screenshots; there is no automated pixel-diff baseline. Other operating systems and real Oracle behavior have not been validated.

Next phase: implement real local authentication/bootstrap and target/schema RBAC, the registry, and encrypted write-only credential storage with a master key outside SQLite. Then implement secure bundle normalization and immutable migration continuity before introducing Flyway execution. Oracle/Flyway compatibility, conditional repair safety, distribution licensing, restore drills, remote HTTPS and cross-platform packaging remain acceptance gates in the product specification.
