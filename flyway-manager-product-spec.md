# Flyway Manager — Product & Technical Specification

**Document version:** 1.0  
**Baseline:** 0000–0007 (agreed); 0008 (UI/UX specification proposal)  
**Date:** 2026-10-15  
**Status:** Design baseline for implementation; technical acceptance gates remain open  
**Product principle:** Flyway is the migration engine. Flyway Manager is a small, standalone, API-first orchestration, identity, credential, history, and audit application.

---

## 1. Executive Summary

Flyway Manager addresses the operational difficulty of using Flyway CLI directly against many Oracle databases and schemas, especially from CI/CD systems. It provides one web application and one vendor-independent REST API to register targets, select credentials, accept SQL migration bundles, execute Flyway, preserve logs and evidence, and expose reliable run results.

**Primary user experience:** install a native application with an npm-distributed launcher, start one local server process, open the web console; no mandatory Docker, PostgreSQL server, or Kubernetes. Multiple authorized users and CI/CD clients access this **single-node, multi-user** installation.

**Non-goals:** build a new migration engine, parse/rewrite arbitrary Oracle SQL, provide a SQL editor or schema diff, create databases, build CI/CD plugins, introduce an approval workflow, automatically retry migration SQL, automatically roll back Oracle DDL, or implement a DBA recovery suite.

## 2. Baseline Decision Register

| ID | Decision | Status |
|---|---|---|
| 0000 | Oracle-first internal Flyway Manager; focused web UI + REST API | Agreed |
| 0001 | Migration input is a collection of SQL files; direct files/folders or ZIP/TAR packages; ZIP preferred for distribution | Agreed |
| 0002 | Each **Database Target + Target Schema** is an independent migration stream and version sequence | Agreed |
| 0003 | Database-first registry; schemas and reusable execution accounts; encrypted local credentials and optional Vault | Agreed |
| 0004 | Central history, immutable run audit, log capture, **conditional automatic Flyway repair** after failed migrate | Agreed, subject to repair safety acceptance |
| 0005 | Local username/password, LDAP/AD, OIDC SSO, API token; Admin/Operator/Viewer RBAC scoped to target/schema | Agreed |
| 0006 | Platform-neutral REST/OpenAPI; asynchronous jobs, idempotency, polling and optional SSE | Agreed |
| 0007 | Standalone single-node distribution; Go server + embedded React UI + SQLite + Flyway CLI; npm launcher; no Docker dependency | Agreed |
| 0008 | Detailed screen-level UI contracts and product acceptance | Defined in this document for implementation, pending visual design acceptance |

## 3. Product Roles and Boundaries

### 3.1 Three identities

- **Application user:** human authenticated through local, LDAP/AD, or OIDC.
- **Service account:** system identity authenticated by scoped API token (CI/CD or other automation).
- **Oracle execution account:** registered Oracle credential selected/assigned for a target schema and resolved only on the server for Flyway execution.

Application identities **must not** receive the Oracle password. A schema is not necessarily an Oracle login account; one Oracle execution account may be mapped to multiple schemas when its database privileges allow this.

### 3.2 Logical resource model

```text
Flyway Manager
  └── Database Target (Oracle service/PDB and environment)
        ├── Registered target schemas
        │     └── Independent migration stream per schema
        │            ├── Version/repeatable migration set
        │            ├── Configured Flyway history location
        │            └── Run history + audit
        └── Oracle execution accounts
              └── Allowed schema associations
```

**Canonical migration-stream key:** `(database_target_id, target_schema_id)`.

- A run selects **one** target schema.
- `CORE_BANKING` version V1 has no relation to `PAYMENT` version V1, even in the same PDB.
- Default Flyway history location: the selected target schema, table `flyway_schema_history`.
- Optional alternative: dedicated history schema/table, explicitly configured for that stream.
- Enforce unique physical history-table location per target; two independent streams must not accidentally share one history table.
- Configuring `flyway.schemas` or `flyway.defaultSchema` does **not** enforce a security boundary. Oracle privileges govern cross-schema changes. Explicit `OWNER.OBJECT` SQL executes against that owner if authorized.
- For a history schema distinct from the target schema, verify `CURRENT_SCHEMA`, unqualified SQL behavior, history privileges, and migration scripts in the Oracle POC.
- Treat PDB/service as the execution target; hostname or Oracle instance alone is insufficient identity.

## 4. Migration Input Contract

**User-facing concept:** `Migration Bundle` — an immutable collection of SQL migration files regardless of upload mechanism.

| Accepted entry | Handling |
|---|---|
| Single `.sql` | Accept directly |
| Multiple `.sql` files | Accept multipart batch |
| Folder upload | Preserve relative directory paths |
| `.zip` | Detect/extract preserving directory hierarchy; recommended archive format |
| `.tar`, `.tar.gz`, `.tgz` | Detect/extract preserving directory hierarchy |
| `.tar.bz2`, `.tbz2`, `.tar.xz`, `.txz`, `.sql.gz` | Optional compatibility formats; enable after tested support |

**Detection:** verify file signatures/actual structure as appropriate; never trust extension alone. Reject malformed archives, absolute/traversal paths, symlinks, hardlinks, nested archives (unless explicitly supported), decompression bombs and unsupported entries. Apply configurable upload size, extracted size, file count, and per-file limits.

**Normalization:** retain original bundle bytes, original filename/type, each normalized SQL path and bytes, SHA-256 checksums, uploader, timestamps, and a generated internal manifest. Users are not required to supply `manifest.json` or `flyway.conf`.

**Flyway-specific contract:** after normalization, reconstruct a temporary filesystem location and allow Flyway to discover/order valid versioned and repeatable migrations (`V...__....sql`, `R__....sql`) using a documented, pinned configuration. Accepting any SQL as a transport file does **not** imply that arbitrary filenames or SQL*Plus syntax are executable by Flyway Community. Do not silently rename or version uploaded files. Unsupported Oracle/SQL*Plus features are flagged; verify the actual Flyway edition/capability matrix with POC.

**Migration continuity:** reconstruct the historical migration set needed by Flyway from the stream's immutable SQL repository plus the submitted bundle. Do not simply run a newly uploaded ZIP in isolation if it makes older applied versions appear missing. Do not silently weaken Flyway validation to hide drift. When a previously applied migration version has different content/checksum, block and report.

**Immutable execution:** a run binds to a fixed artifact and frozen target/stream/configuration snapshot. Worker creates an ephemeral workspace, invokes Flyway, then securely removes the workspace. No permanent arbitrary user-controlled execution path.

## 5. Execution & Conditional Auto Repair

### 5.1 Job lifecycle

```text
UPLOAD → NORMALIZE/VALIDATE → READY
  → SUBMIT (authenticated, authorized, idempotent)
  → QUEUED → PREFLIGHT → RUNNING
                         ├─ migrate SUCCESS → SUCCEEDED
                         └─ migrate FAILED → PERSIST ERROR/AUDIT
                                               → inspect history/safety
                                               → REPAIRING (if eligible)
                                               → verify/audit repair
                                               → FAILED (pipeline stays FAILED)
```

Additional terminal/holding states: `BLOCKED`, `NEEDS_REVIEW`, `CANCELLED` (only prior to execution). Record migration outcome and repair outcome as separate fields/entities. A successful repair **never** changes a failed migration run to succeeded.

### 5.2 Concurrency

- At most one active `migrate → conditional repair` sequence **per database target + schema**.
- This is an **application-level job serialization**, not a permanent Oracle lock or a demand to hold a database table lock.
- Do not dispatch Run B between Run A's failed migrate and its repair: otherwise Run B encounters an avoidable failed-history condition or races with repair.
- Single-node implementation can use a transactional SQLite job claim and a per-stream in-process mutex/active-job lease with startup reconciliation; do not add Redis or a separate locks service.
- Never assume separate streams cannot conflict when SQL has cross-schema DDL; shared physical-resource interference is possible and should be documented/operationally controlled.

### 5.3 Failure/repair rules

1. Capture process exit code, stdout/stderr, Flyway structured result, timestamps, migration identity, SQL snapshot/checksums, and audit **before** any repair.
2. Determine whether the failure represents a Flyway failed-history record; not every nonzero exit justifies repair (e.g., authentication or package validation failure).
3. Invoke `flyway repair` automatically **only if safety conditions are satisfied** and using the identical stream/history configuration and complete migration locations.
4. Preserve before/after history evidence and record `repair_operations` independently of `migration_runs`.
5. Flyway `repair` can do more than remove failed rows: it can realign checksums and mark missing migrations deleted. Avoid unconditional repair if unrelated history changes are possible. The current CLI may not provide an atomic, failed-version-only repair; the guardrail requires an executable POC.
6. **No SQL retry** and **no rollback** after repair.
7. Return a FAILED result to the API caller/CI/CD even if repair succeeds.
8. If repair is unsafe, fails, or result is uncertain, retain failure evidence and report `BLOCKED`/`NEEDS_REVIEW`; do not mislabel schema as safe.
9. A repaired Flyway history does **not** reverse partially committed Oracle DDL. Subsequent migration may still fail due to partial changes. Communicate this explicitly in UI/API.
10. Worker crash/ambiguous outcome: reconcile against Oracle history before any new execution; never re-dispatch a potentially executed migration automatically.

### 5.4 Suggested Flyway guardrails (verify pinned version)

`validateOnMigrate=true`, `outOfOrder=false`, `cleanDisabled=true`, `baselineOnMigrate=false`, `createSchemas=false`; `schemas`, `defaultSchema`, `table` and `locations` fixed per run/stream. Prefer structured JSON where supported and ensure CLI versions/options are tested, not assumed.

## 6. Authentication, Authorization and Credential Management

### 6.1 Authentication modes

| Mode | Behavior |
|---|---|
| Local | Username/password, password hash Argon2id; optional emergency admin with hardening |
| LDAP/AD | Direct LDAP bind over LDAPS or StartTLS, no persistent LDAP passwords |
| OIDC SSO | Standards-compliant authorization code + PKCE, e.g. Keycloak/Entra ID |
| API token | Bearer token linked to service account, hashed at rest; expiration, scoped permissions, rotate/revoke |

Providers may be enabled/disabled independently; never silently fall back between LDAP and local identity of the same username. Bind external identities using immutable issuer/provider plus subject ID, not just email or display username. New identities receive no privileged access by default.

### 6.2 Roles

| Action | Admin | Operator | Viewer |
|---|---:|---:|---:|
| View granted target/schema/history/logs | Yes | Yes | Yes |
| Upload/submit migration on granted target/schema | Yes (explicit PROD permission) | Yes | No |
| Manage Oracle targets/accounts/credentials | Yes | No | No |
| Manage users/service accounts/permissions | Yes | No | No |
| Inspect repair audit | Yes | Yes (scoped) | Yes (scoped) |
| Change auto-repair policy | Yes | No | No |

All permissions are evaluated by backend on `(actor, action, target_id, schema_id)`; PROD execution is never enabled implicitly. Restrict download of SQL content separately from reading run metadata when policy requires.

### 6.3 Database registry and accounts

Each Oracle target stores a stable ID, label, environment, host/port/service (or supported connect descriptor), transport/security settings, and enabled state. Schemas are separately registered/discovered. Execution accounts belong to target(s) as designed; account-to-schema association is explicit and may be many-to-many. Allow a schema to have a default account and zero or more alternative authorized accounts.

**Secret storage default:** encrypted values in local SQLite using authenticated encryption (e.g., AES-256-GCM) with a master key managed **outside the database**, protected by OS file permissions and backed up separately. Support Vault/external secret reference via adapter; Wallet may be deferred. Password never returned to UI, echoed into logs, passed in argv, or embedded in stored JDBC URLs. Validate chosen Flyway credential-injection method for leaks (environment/stdin/temporary config) and sanitize diagnostics.

Connection test should report reachability, authentication status, and scoped privilege checks where feasible; do not claim to prove absence of all cross-schema powers from a superficial probe.

## 7. CI/CD-Neutral REST API (Baseline 0006)

Use `/api/v1`, HTTPS for remote clients, OpenAPI 3.1, structured error codes, pagination, audit of administrative mutations, and token-scoped authorization.

| Method | Resource | Purpose |
|---|---|---|
| GET | `/database-targets` | List authorized targets |
| GET | `/database-targets/{id}/schemas` | List authorized schemas |
| POST | `/artifacts` | Upload and normalize SQL bundle |
| GET | `/artifacts/{id}` | Validate/inspect uploaded artifact |
| POST | `/migration-runs` | Queue a run referencing artifact, target, schema and execution account |
| GET | `/migration-runs` | Search/filter history |
| GET | `/migration-runs/{id}` | Status, metadata, repair outcome |
| GET | `/migration-runs/{id}/logs` | Paginated/downloadable logs |
| GET | `/migration-runs/{id}/events` | Optional SSE stream |
| GET | `/migration-runs/{id}/artifacts` | File manifest/checksums and permitted download |
| Admin endpoints | `/execution-accounts`, `/access/*`, `/settings/*` | Secure management |

Example submit:

```http
POST /api/v1/migration-runs
Authorization: Bearer <SERVICE_ACCOUNT_TOKEN>
Idempotency-Key: deploy-core-uat-1042
Content-Type: application/json
```

```json
{
  "target_id": "oracle-uat",
  "schema_id": "core-banking",
  "execution_account_id": "deploy-admin",
  "artifact_id": "art_82fd21",
  "context": {
    "external_run_id": "pipeline-1042",
    "commit_sha": "a16bc0",
    "initiated_by": "caller-supplied-metadata"
  }
}
```

Return HTTP `202` and a `run_id`, status URL, and logs URL. Same actor + idempotency key + identical canonical request payload returns the same `run_id`; same key with conflicting payload returns `409`. The key is durable for the documented retention period. API identity from the token is authoritative; `initiated_by` is untrusted external metadata, not authenticated identity.

**CI/CD result mapping:** `SUCCEEDED` -> pass; `FAILED`, `BLOCKED`, `NEEDS_REVIEW`, `CANCELLED` -> not pass. Queued/running/repairing remain nonterminal. A repaired failed run stays failed.

Optional webhook/callback may be added after polling/SSE baseline; no Jenkins/GitLab-specific API fields or hard-coded integrations.

## 8. Standalone Technology Architecture (Baseline 0007)

### 8.1 Chosen stack

| Layer | Technology |
|---|---|
| Web UI | React + TypeScript + Vite; Tailwind + shadcn/ui; TanStack Table/Query |
| Server/API | Go with `net/http` + chi; typed API contract/OpenAPI |
| Execution | Go subprocess runner invoking pinned Flyway CLI |
| Persistent store | SQLite in WAL mode, one server writer; relational tables + BLOBs |
| Flyway runtime | Official, version-pinned CLI distribution with compatible JRE and Oracle JDBC support |
| Distribution | npm-published cross-platform installer/launcher plus native Go binaries |
| Deployment default | One native server process on Linux/Windows/macOS where supported; no Docker |
| Optional deployment | Docker later, strictly optional |

**Monorepo, not microservices.** Embed built React static assets in Go server. Execute Flyway as an isolated child process for jobs; no always-running worker container/service is required. The job queue lives in SQLite and is consumed by the same server process. If future needs require horizontal scaling, reassess storage and queue; do not implement distributed systems now.

### 8.2 Runtime diagram

```text
npm launcher: flyway-manager
           |
           v
Native Go server (single process)
  +-- React UI (embedded static assets)
  +-- REST API / Local-LDAP-OIDC-Token auth / RBAC
  +-- Artifact normalization and run job queue
  +-- Flyway subprocess orchestration
  +-- Central run logs, audits, conditional repair
           |
           +--> SQLite manager.db (metadata + bundle BLOBs + logs + audit)
           +--> master encryption key (separate protected file/OS secret)
           +--> pinned Flyway CLI/JRE/JDBC installation
                                |
                                v
                   Oracle target service/PDB
                   target schema + its Flyway history
```

### 8.3 Installation experience (target; package name not reserved/verified)

```bash
npm install -g flyway-manager
flyway-manager
```

The first launch initializes a protected application data directory, SQLite schema, encryption-key handling, Flyway runtime setup/verification and an initial Admin bootstrap wizard. Later launches reuse state. Server binds to `127.0.0.1` by default; remote multi-user access requires explicit binding, trusted HTTPS and network controls.

Additional commands (planned): `init`, `start`, `stop`, `status`, `doctor`, `backup`, `restore`, `upgrade`, `version`. For Linux production-like internal hosting provide a service-install example for `systemd`; Windows Service support can be staged by platform acceptance priority. CLI should not require Git clone, Go toolchain, Docker, or a preinstalled Java runtime.

**Distribution caveat:** validate upstream license/redistribution rules and checksums for Flyway runtime, JRE and Oracle JDBC. Prefer downloading verified upstream distributions on first run if repackaging is not permitted. `npm install` alone cannot guarantee zero dependencies on every OS without supplying appropriate binaries and supported installation paths.

### 8.4 Data directory and SQLite

```text
~/.flyway-manager/
  config.yaml
  data/manager.db
  secrets/master.key   # or better OS-provided secret/keychain
  runtime/flyway/      # verified pinned Flyway distribution
  tmp/                 # ephemeral execution workspaces
```

Paths should be OS-native/configurable; use service-specific directories for server deployments, restrictive permissions, and explicit ownership. SQLite WAL still requires correct backup (online backup API or consistent checkpoint/snapshot), integrity checks, disk space monitoring and recovery tests. **Database backups alone are not enough without the separate encryption key.**

### 8.5 Storage schema (logical)

| Table | Responsibility |
|---|---|
| `users`, `user_identities`, `roles`, `permissions` | Human identities and RBAC |
| `service_accounts`, `api_tokens` | Automation identities and hashed tokens |
| `database_targets`, `target_schemas` | Oracle registry |
| `execution_accounts`, `account_schema_grants`, `credential_secrets` | Oracle accounts and encrypted credentials |
| `migration_streams` | Target + schema, history location, execution settings |
| `artifacts`, `artifact_blobs`, `migration_files` | Original bytes, normalized SQL bytes, checksums, manifest |
| `migration_runs`, `migration_run_items` | Immutable run/request snapshots and results |
| `migration_logs` | Sequence-stamped stdout/stderr/events |
| `repair_operations` | Independent before/after repair audit |
| `audit_events` | Administrative/security events |
| `idempotency_records` | Durable deduplication of API requests |
| `jobs` | Persistent execution queue/claim state |

Use SQLite `BLOB` for original ZIP/TAR and normalized SQL. Keep large blobs out of ordinary list queries by storing separate blob rows. Suggested initial configurable limits (to performance-test): max upload 50 MB, extracted total 200 MB, 1,000 SQL files per bundle, individual SQL 20 MB, log budget 50 MB per run. These are **proposed configuration defaults**, not SQLite hard limits. Plan retention/backups before production use.

## 9. User Interface — Baseline 0008 Functional Contracts

Keep an operations console with compact left navigation. **Run History is home.** No decorative KPI dashboard. Clearly display environment, database and target schema on every execution screen. Screen-level visual design is pending Stitch review; functional scope below is fixed proposal.

### Screen A — Run History (home)

- Table columns: Run ID, status, environment, database, target schema, artifact filename, requester/actor type, execution account, started at, duration, repair result.
- Filters: status, target, schema, actor/requester, date range, source (WEB/API), free-text/run ID.
- Pagination and deep-link to Run Details; accessible status labels (not only colors).
- Do not conflate FAILED migration + SUCCEEDED repair with successful migration.

### Screen B — Run Migration

- Select registered database/PDB; select authorized target schema; select mapped execution account (default preselected).
- Upload a `.sql`, multi-file selection, folder, ZIP or supported TAR variant; detect input automatically.
- Show file manifest, original size and checksum, SQL count, validation errors, Flyway-discovered migration preview/version sequence and prior state.
- Explicit confirmation contains environment, target service, schema, credential account name (never password), and immutable artifact ID.
- Submit async; navigate to the Run Details progress/log view.

### Screen C — Run Details

- Run ID, actor identity, requested-by provenance, target/schema/account, Flyway version, file/checksum, timestamps, exit code and current status.
- Live/paginated searchable logs with download; distinguish stdout, stderr and structured events.
- Show migration failure separately from automatic repair outcome and before/after evidence.
- On failed repair or uncertainty, explain why run/stream cannot be assumed safe. No manual SQL rollback, manual repair workflow or blind retry button in MVP.

### Screen D — Databases & Schemas

- Registry of database target connections (service/PDB/environment); health/connection test.
- Target schemas, individual migration stream configurations, history schema/table location.
- Execution account management and schema mappings; encrypted/secret-reference provider; mask credentials and allow rotation without readback.
- Avoid conflating admin account privileges with schema access: warning when using broadly privileged credentials.

### Screen E — Users & Access

- Local users, external LDAP/OIDC identity mappings, Admin/Operator/Viewer roles, per-target/schema grants.
- Service account creation and API token issuance (display only once), scope, expiry, revoke/rotation.
- Authentication provider configuration available only to Admin.

### Login

- Distinct local login and LDAP login options (where enabled), plus SSO button; service API tokens **not** entered into the browser login UI.

### Reusable UI states

`LOADING`, `EMPTY`, `READY`, `VALIDATING`, `QUEUED`, `RUNNING`, `REPAIRING`, `SUCCEEDED`, `FAILED`, `BLOCKED`, `NEEDS_REVIEW`, `UNAUTHORIZED`, `CONNECTION_ERROR`.

## 10. Testing and Acceptance Gates

No full implementation should be declared production-ready without evidence for these cases:

1. **Flyway Oracle compatibility:** pinned CLI + matching Oracle JDBC; Oracle 19c/21c; supported DDL/PLSQL cases; rejected unsupported SQL*Plus constructs.
2. **Independent schema versioning:** `CORE_BANKING/V1` and `PAYMENT/V1` must be unrelated; same admin account can be reused if Oracle grants permit; history location correct.
3. **Named vs unqualified SQL:** verify Oracle `CURRENT_SCHEMA` semantics, especially dedicated history schema; do not misrepresent Flyway configuration as a privilege sandbox.
4. **Bundle continuity:** V1/V2 historical files + new V3; repeatables; checksum drift; missing applied version; nested folders; malformed ZIP/TAR; extraction attacks.
5. **Idempotency:** client timeout then identical retry returns same Run ID; conflicting reuse returns 409; no duplicate dispatch.
6. **Concurrency:** two requests targeting same schema serialize; no second run enters between migration failure and repair; separate schemas can progress independently when safe.
7. **Failure/repair:** Oracle DDL partial commit; failed-history `success=false`; capture logs and immutable error first; conditional repair; verify history; return FAILED despite successful repair; ensure no unrelated checksum/missing-migration changes.
8. **Interrupted execution:** crash during migration/repair, reboot, uncertain Flyway/Oracle state; reconcile, no automatic SQL re-execution.
9. **Credential security:** secret encryption/rotation/backup, no password in URLs/argv/logs/HTTP responses, token scope enforcement, LDAP/TLS and OIDC checks.
10. **SQLite durability:** concurrent reads/log streaming, WAL crash recovery, encrypted-secret key backup, restore drill, BLOB sizing, retention and schema upgrades.
11. **Standalone packaging:** clean install on supported target OS/architecture, first-run initialization without Docker/Java preinstall, signed/checksummed downloads, upgrade/downgrade safeguards, service restart.
12. **API/UI:** browser E2E for five screens; RBAC boundary tests; polling and SSE, log download, no misleading success after auto-repair.

### Explicit unresolved implementation validations (not new product modules)

- Exact Flyway version/edition/JRE/JDBC compatibility, SQL*Plus limitations and redistribution/licensing terms.
- Practical guardrails for automatic `repair` when its effects can include checksum realignment and missing migrations. If safety cannot be proven, skip repair and record BLOCKED/NEEDS_REVIEW.
- Final supported operating systems/architectures and first-release npm package name/namespace.
- SQLite capacity limits and retention settings validated against real migration-bundle/log volumes.
- Remote HTTPS/service management deployment procedure for internal shared use.

## 11. Delivery Plan

| Phase | Deliverable | Exit criteria |
|---|---|---|
| P0 — Design closure | UX screens in Stitch, API OpenAPI spec, finalized state model | Product flow approved; no functional scope creep |
| P1 — Standalone foundation | Go server + embedded React + SQLite schema + installer/bootstrap | Clean install/start; persisted settings; health checks |
| P2 — Registry & identity | Database/schema/account registry, encrypted secrets, local auth + RBAC | Credential isolation and target-scoped access tested |
| P3 — Flyway execution | Bundle normalizer, version repository, Flyway subprocess, async run queue | Oracle lab happy path + idempotency + schema isolation |
| P4 — Operations audit | Run history, logs, error/repair audit, conditional auto repair | Failure acceptance and ambiguous-state handling passed |
| P5 — Enterprise auth & delivery | LDAP/OIDC, service tokens, installation/upgrade/backup, UI polish | E2E, packaging, security and restore gates passed |

Phases are engineering sequencing, not a mandate to expand MVP scope. CI/CD platforms integrate only through published API; no platform-specific plugin is required.

## 12. Explicitly Out of Scope

- New SQL execution engine or custom Oracle SQL/SQL*Plus parser/re-writer.
- SQL editor, schema diff, drift remediation platform, approval workflows or database provisioning.
- Running multi-schema bundles as a single shared version stream.
- CI/CD vendor adapters, pipelines or custom Jenkins/GitLab integrations built into product.
- Automatic migration retry, DDL rollback, arbitrary DBA repair UI.
- Mandatory Docker, Kubernetes, Helm, GitOps, external PostgreSQL, Redis, Kafka, Vault, S3 or MinIO.
- Multiple active manager server replicas / distributed execution in the first release.

## 13. Final Architecture Statement

> Flyway Manager is a standalone, single-node, multi-user Oracle migration operations console and REST service. It accepts SQL migration bundles, manages Oracle database/PDB and schema-scoped execution, stores encrypted credential references and all application data in SQLite, invokes pinned Flyway CLI on demand, maintains separate migration histories per target schema, exposes immutable run/log/audit evidence, and conditionally performs automatic Flyway repair after a failed migration without re-executing SQL or converting a failed run into success. It ships as one native Go application with embedded React UI and an npm-distributed launcher, requiring no Docker runtime by default.
