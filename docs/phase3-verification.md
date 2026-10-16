# Phase 3 implementation and verification

**Classification: IMPLEMENTED — LIVE ORACLE ACCEPTANCE PENDING.** Local implementation and controlled fixture acceptance target Linux amd64 on 2026-10-16. Oracle 19c and Oracle 21c are independently NOT_VERIFIED. No live Oracle credentials or authorized disposable Oracle lab were supplied, and no live migration/repair was attempted. No deployment, push, merge, external infrastructure change, production credential use, or Stitch project mutation occurred.

The starting repository was clean at `08df673` on `master` (Phase 2). The complete canonical specification, Phase 2 implementation/report, identity/RBAC/registry/credential/audit code, migration ledger, and approved Stitch project were inspected. Existing Phase 2 components and migration files 001/002 were preserved. Baseline `make check`, race tests and seven Chromium tests passed before execution integration. This report is verification evidence, not a replacement product specification.

## Slice acceptance

| Slice | Implemented behavior | Local acceptance | External gate |
| --- | --- | --- | --- |
| 3A Runtime | Complete distribution manifest pin; exact CLI version; Java/JDBC/module availability; protected subprocess; optional atomic offline install of approved files; authenticated runtime readiness | Fake executable integrity/version/dependency, escaping/redaction, structured result, timeout/process cancellation, installer/tampering tests | Approved real Flyway edition/version/JRE/JDBC combination, acquisition/redistribution rights and Oracle compatibility |
| 3B Artifacts | SQL/multiple files/folders/ZIP/TAR/TAR.GZ/TGZ; server checksums; exact input and normalized SQL BLOBs; immutable manifests/IDs; bounded multipart reads; scoped retrieval/download | Format/byte-preservation, path/link/special entry/nested archive/ratio/count/size/depth/CRC tests; API/browser upload and download | Real bundle capacity/performance and retention/backup acceptance |
| 3C Preflight | Current identity/grants/PROD, activity, mapping, credential, frozen files/config, stream pin/conflicts, historical continuity; Flyway info/validate with machine-readable blockers | Version collisions/drift, V1→V2 continuity, repeatable updates, missing resolver files, pending historical retry prevention and scoped API tests | Real Oracle network/authentication/service/schema/history/privileges and parser behavior |
| 3D Jobs | Durable 202 submissions/idempotency; four worker slots; per-stream claims/serialization; frozen run snapshots; process/evidence persistence; queue recovery; read-only uncertain-run reconciliation | Concurrent submission, separate-stream progress, full transition matrix, queued authority/credential changes, cancel dependencies, native restart/crash and race tests | Actual DDL interruption, storage failure/power-loss/capacity drills |
| 3E Repair | Original failed migration committed before inspection; exact single failed-version eligibility; one repair; no checksum alignment/deletion; complete post-repair view; failed migration remains failed | Eligible/unsafe failure, absent/altered post-history, unrelated validation changes, no implicit SQL retry, browser failure/repair evidence | Executable Oracle partial-commit/repair POC and external-writer exclusion |
| 3F UI/API | Run Migration/History/Details use real backend; file/folder upload, engine preview, preflight, explicit confirmation, idempotent submit, status/log polling, filters/pagination, evidence/downloads | Production build/typecheck/lint/unit tests; browser upload/unavailable-runtime plus controlled subprocess success/failure flows; OpenAPI validation | Vendor environment acceptance and independent production security review |

## Runtime and safety model

No real Flyway version is silently selected or bundled. `config.example.json` leaves executable/version/manifest pin empty; fresh runtime readiness fails closed. Controlled tests pin fixture versions (10.0.0, 10.20.0 and 11.20.0), which are test identifiers rather than proof that an upstream distribution was installed or accepted.

`internal/flyway` verifies the SHA-256 of an operator-approved `flyway-manifest.sha256`, then every regular distribution file. Unlisted/missing/changed files, symlinks and invalid paths fail. The approval/provenance channel for this pin must be established outside the application; computing a new digest from arbitrary files does not approve those files. `runtime-install` performs a bounded, protected, staged copy and validation, fsync and atomic no-replace publication under the application runtime directory. It downloads nothing and does not infer license entitlement.

The adapter permits only info/validate/migrate/repair with explicit argv. Passwords live in a private config, never argv; inherited Flyway/Java/cloud settings are removed. Workspaces/config/SQL use private Unix modes. Process output and JSON redact passwords, escaped/encoded password variants and sensitive JSON fields, including JSON-escaped values in stdout/stderr. CLI stdout/stderr are persisted when a command completes; status is durable before launch. Output still buffered inside an abruptly killed manager is not recoverable; restart reconciliation adds independent history evidence. Runtime verification and commands share a four-slot capacity bound across HTTP callers and workers. Java heap is capped at 512 MiB; process output is capped at 2 MiB per stdout/stderr. Worker command timeout defaults to 300 seconds, maximum 3,600. Synchronous HTTP preflight/runtime requests have a separate 50-second total budget. Unix cancellation kills the subprocess group. Process exit zero alone cannot establish success: exact version, operation and command-specific structured evidence are required; post-migrate info/validate must resolve the complete snapshot and show no pending versioned migrations.

Generated config keeps clean disabled, createSchemas/baselineOnMigrate/outOfOrder/placeholders/SQL*Plus disabled, and naming/validateOnMigrate enabled. `ignoreMigrationPatterns=*:pending` explicitly permits new pending files (and Flyway's expected repeatable update behavior) during pre-migration validation; missing/future/failed/type/name/versioned checksum validation is not suppressed. Flyway's own resolver, parser, checksums and Oracle history remain authoritative. No custom SQL parser, SQL rewrite, SQL retry or rollback exists.

The documented info fields `filepath` and `installedOnUTC` are supported, with compatibility for script/installedOn fixture fields. Evidence comparisons normalize ephemeral workspace path prefixes to script names while retaining original JSON. Resolver coverage ensures a superficially successful empty-location command cannot mark uploaded SQL as executed.

Readiness is local-only. Successful preflight info/validate contacts the configured service through Flyway. Network/authentication/service evidence, history inspection, schema/DDL privilege uncertainty and DBID/service-identity uncertainty are reported separately. Known Oracle error categories distinguish listener/network, authentication, service selection, absent/inaccessible history and insufficient privileges; the classification tests use controlled diagnostics. An unknown/timeout outcome stays NOT_VERIFIED. No universal privilege or cross-schema sandbox claim is made.

## Persistence, continuity and lifecycle

Additive `003_execution.sql` creates `artifacts`, `artifact_blobs`, `migration_files`, `migration_streams`, `stream_files`, `migration_runs`, `jobs`, `migration_run_items`, `idempotency_records`, `run_executions`, `migration_logs`, `repair_operations` and `run_dependencies`, with foreign keys, indexes, immutable-evidence triggers and a legal-transition trigger. Original bundle and normalized SQL bytes remain in SQLite BLOBs. Ordinary history queries do not load BLOB content. Existing installation IDs, keys, sessions, tokens, grants and credentials survive the additive upgrade.

Each `(target_id,schema_id)` pins its connection/history and runtime identity. A run freezes target/schema/account metadata, credential version, exact nonsecret Flyway option policy, requester identity, caller context and complete file references. Credentials are decrypted only on the server. Current session/token/owner/role/grants/PROD/activity/mapping/config are rechecked transactionally before dispatch. Service identity is attributed from the authenticated token, not initiated_by/commit/external context.

Run states are QUEUED, PREFLIGHT, RUNNING, REPAIRING, SUCCEEDED, FAILED, BLOCKED, NEEDS_REVIEW and queued-only CANCELLED. Failed migration and repair outcomes are distinct. SQLite claims, a single-manager OS lock and stream mutexes retain ownership through failed outcome persistence and repair. Independent streams may progress concurrently. These locks do not exclude an external Flyway/DBA writer or Oracle cross-schema interference; those require operational control.

Historical versioned files are reconstructed with new uploads; changed reserved versioned bytes/paths fail. Repeatables may replace the stream's current reference while prior artifacts/run snapshots remain immutable. Queue dependencies prevent a later job from executing SQL inherited from a cancelled/failed predecessor. Unused cancelled/blocked reservations are discarded from the mutable stream index. Historical Pending versioned SQL must also occur explicitly in the submitted artifact: uploading an unrelated Vn cannot implicitly retry failed SQL. Idempotency is scoped to principal kind/ID plus canonical request, persists across restart and has no automatic expiry/pruning.

A restart may dispatch an unclaimed QUEUED job. Any uncertain claimed/in-flight job holds its stream and becomes NEEDS_REVIEW; dependent queued jobs block. When current authorization, unchanged snapshots and runtime allow, startup runs only info/validate and persists reconciliation evidence. It never replays DDL, invokes startup repair, manufactures an exit code or clears an uncertain hold. Partial-DDL remediation and the procedure for resuming held streams remain operational acceptance work; there is no arbitrary hold-clear/SQL/manual-repair/retry API.

Conditional repair requires exactly one originally pending failed versioned row, identical unrelated applied history, and a corresponding sole FAILED_VERSIONED_MIGRATION error from the pinned Flyway validator. Repeatable/superseded history, newly applied additional migrations during the failed attempt, missing evidence or other validation changes make repair unsafe. It uses the exact same frozen target/history/files once. The result must remove exactly the failed version, align/delete nothing, validate successfully, and restore the full pre-migrate resolver view with the failed candidate Pending. Failure/ambiguity holds the stream. A successful repair preserves FAILED and its exit code and returns a non-pass outcome to polling automation. Repair never undoes Oracle DDL.

## API and UI delivery

The OpenAPI version is 0.3.0. Implemented routes include runtime readiness; scoped mapped-account selection; artifact multipart upload/manifest/input/SQL retrieval; migration preflight; 202 migration submission with required Idempotency-Key; filtered history; run details/logs/artifact references/executions/repair/downloadable record; and queued cancellation. Browser requests retain Origin/CSRF, bearer requests retain scoped service authority. All run/artifact/evidence/download reads enforce current target/schema policy. List authorization happens before pagination. SSE is optional and not implemented; progress/logs use authenticated polling. Unknown events routes are not advertised.

The operations screens reuse approved navigation, components and design tokens. They show real loading/empty/error/unavailable/blocked states, credentialed mapped accounts, server manifests and Flyway-ordered preview, explicit destination confirmation, payload-bound idempotent retry, server-filtered/paginated history, migration versus repair outcomes and downloadable evidence. No fixture records or simulated Oracle success exist in the application. Test-only fake binaries live in isolated temporary directories. A separate history schema is deliberately blocked pending its CURRENT_SCHEMA acceptance.

## Verification evidence

Commands are run against temporary fixtures and embedded assets; no existing live installation is upgraded by the tests.

- `make check`: production TypeScript/Vite/native build, Go vet and unit/integration/native-process tests, ESLint and frontend contract/security unit tests.
- `make test-race`: Go race detector across command, artifact, config, core, SQLite, Flyway, HTTP, identity provider and secret packages.
- `make test-e2e`: original identity/registry/access/responsive coverage plus isolated real upload/unavailable runtime and controlled fake-executable success/failure/repair workflows. Controlled success is application orchestration evidence, not Oracle evidence.
- Native process tests invoke the compiled manager and an integrity-pinned fake executable, cover documented Flyway fields, HTTP upload/preflight/submission/logs, idempotency across restart, SIGKILL during migration, dependent-job blocking and no repeated migrate.
- Core tests cover concurrent idempotency, principal attribution, serialization/independent progress, complete state transitions, snapshots/credential/grant revocation, pending historical retry prevention, repeatable history, cancelled predecessors, scoped/inactive history, immutable bytes/evidence, repair eligibility/restoration and read-only reconciliation.
- Archive tests cover supported formats, exact bytes, canonical version/repeatable collisions, traversal/absolute/backslash/control/depth paths, links/special entries, nested archives, upload/expanded/count/ratio limits, gzip checksum/trailer/concatenation attacks.
- Flyway tests cover complete-tree integrity/version/dependency checks, safe argv/config/env, redaction, strict JSON/result verification, bounded output/timeout and offline install/tamper/no-replace behavior.
- SQLite regression tests preserve legacy migration checksums/data, reject ledger edits/gaps/checkpoint drift, roll back failing future migrations, and recover committed WAL after process exit. Phase 2 API/native auth/registry/credential tests remain functional.
- OpenAPI 3.1: Redocly recommended validation passes with only the existing info-license and operation-4xx-response style rules excluded; structural validation remains enabled.

Final local results: production native/embedded build, typecheck, Go vet, Go suite (101 top-level tests plus table cases), ESLint and 11 frontend unit tests pass. All 10 Chromium tests pass. The final Go 1.26.9 race suite passes, including five repeated controlled native success/crash cases after fixing a test that previously inspected startup reconciliation before its persisted validate result completed. OpenAPI structural validation and `git diff --check` pass.

`make security` reports zero npm vulnerabilities and zero reachable or imported-package Go vulnerabilities with the pinned Go 1.26.9 toolchain. Verbose scan identifies one module-only advisory, GO-2026-5932, for unimported `golang.org/x/crypto/openpgp`; this application uses Argon2 and does not import OpenPGP. A supplementary unpinned host-toolchain scan used Go 1.26.5 and reported standard-library advisories; that result is not accepted for release builds. Make enforces Go 1.26.9, and the final native binary's build metadata confirms Go 1.26.9. Use the pinned build/check commands rather than the older host toolchain. Security scans do not replace penetration testing or Oracle acceptance.

No vendor-specific Oracle behavior is inferred from fixture results.

## Oracle and distribution acceptance matrix

| Gate | Oracle 19c | Oracle 21c | Required evidence |
| --- | --- | --- | --- |
| Connectivity/auth/service/PDB identity | NOT_VERIFIED | NOT_VERIFIED | Explicitly authorized disposable lab; pinned CLI/JRE/JDBC; real connection and independently checked identity |
| Schema access/history placement/defaultSchema | NOT_VERIFIED | NOT_VERIFIED | Current schema, unqualified and named DDL, same-schema history and dedicated-history behavior |
| PL/SQL blocks and package bodies | NOT_VERIFIED | NOT_VERIFIED | Versioned/repeatable scripts executed by the chosen Flyway edition without rewriting |
| Cross-schema privileges and implicit commits | NOT_VERIFIED | NOT_VERIFIED | Minimal account grants, blocked unauthorized changes, partial-DDL failure evidence |
| Conditional repair safety | NOT_VERIFIED | NOT_VERIFIED | Before/after real history, failed entry removal, no unrelated alignment/deletion, no SQL retry and failed automation result |
| TCPS/trust/driver/version compatibility | NOT_VERIFIED | NOT_VERIFIED | Selected trusted distribution and real transport/CA configuration |

`@`/`@@` includes, `&`/`&&` substitution, WHENEVER and SPOOL are not preprocessed or enabled. Their exact edition behavior is NOT_VERIFIED; SQL*Plus is disabled. PL/SQL and package bodies are preserved verbatim for Flyway. Oracle cross-schema DDL permissions are the security boundary; history repair does not reverse implicit commits. See the official-source findings in [the runtime adapter notes](../internal/flyway/README.md).

No real Flyway/JRE/JDBC distribution was redistributed or granted a license by this task. Upstream checksums/provenance/entitlement and real compatibility must be approved before use. Linux local fixture acceptance does not establish Windows/macOS ACL/process-tree/launcher behavior. Single-node storage capacity, log/artifact retention, master-key backup/restore, crash leftovers, disk-full/power-loss recovery and an operational held-stream recovery procedure require acceptance before production.

Phase 4 engineering can build on the implemented execution/audit interfaces. Production use remains gated by the independent Oracle and operational evidence above.

## Publication verification and isolated Oracle lab preparation (2026-10-16)

The publication task reverified the existing Phase 2 commit `08df67309f350ff3be9fda3288ac349cc2332bf4` and Phase 2/3 implementation commit `89670d478ee0fa8bcebed25ce617b9bee8f2a937` without rewriting either commit. The implementation working tree was clean. GitHub's default branch is `master`, with no configured Actions workflows; its head before publication was the Phase 2 commit. Publication does not deploy Flyway Manager.

Repeated `make check` passes (production embedded/native build, Go vet and Go tests, ESLint, and 11 frontend tests). `make security` reports zero npm vulnerabilities and zero reachable/imported-package Go vulnerabilities, with one module-only advisory. Redocly recommended OpenAPI validation passes with the same two style exclusions documented above. Gitleaks 8.30.1 scans all six existing commits without findings. A separate inspection of all 225 historical Git blobs finds no SQLite, executable/archive runtime signatures or sensitive credential/configuration filenames; the current 152 tracked files also pass. Generated native binaries, embedded build output, dependencies and browser results remain ignored. These scans establish repository hygiene, not live Oracle compatibility.

The publication rerun of `make test-race test-e2e` also passes: the full Go race suite and all 10 Chromium tests, including the original identity/registry flows and controlled migration/repair fixtures. The native binary build metadata confirms Go 1.26.9. No product code changed during publication verification.

### Provisioning gate

Read-only inspection confirmed context `k8s-admin-public`, cluster `k8s-public`, administrator permissions, and multiple existing application workloads. The context defaults to namespace `argocd`; it must never be used implicitly for this task. Neither administrator permissions nor a lab hostname establishes that new resources are approved or disposable.

Observed storage includes `longhorn-workers` (two replicas, Retain reclaim policy) and `longhorn-static` (Delete reclaim policy). An existing, unrelated Oracle Free workload has a Bound 4 GiB PVC and a ClusterIP Service. It was inspected only through metadata, image and resource fields; no credentials were retrieved, SQL run, or existing workloads modified. Its Service is not the isolated Flyway Manager acceptance endpoint.

The following concrete proposal requires approval from the lab owner before any resource creation:

| Item | Proposed scope; not deployed or approved |
| --- | --- |
| Context | `k8s-admin-public`, explicitly confirmed for this isolated disposable workload |
| Namespace | New `flyway-manager-oracle-lab`; every subsequent operation explicitly selects it |
| Workload | One Oracle pod on Linux amd64 workers; no changes to existing workloads |
| Storage | New 4 GiB RWO PVC using `longhorn-workers`; account for two replicas and retained storage; no cleanup/deletion is authorized |
| Resources | Request 250m CPU / 2 GiB memory; limit 2 CPU / 3 GiB memory; recheck available worker and storage capacity before creation |
| Candidate image | `gvenzl/oracle-free:23.26.3-slim@sha256:6d61d267a3b978c24c5ac1790e62e927416a0aec446bd86e4b3a1527562757bd`, observed in the cluster; verify registry provenance and lab approval before acquisition |
| Exposure | ClusterIP only, TCP 1521; no ingress, NodePort or LoadBalancer |
| Credentials | New random secrets, mounted privately; never reuse existing credentials, print passwords in logs, or commit Secret manifests |
| Tests | Connection, identity/version, history inspection, effective account grants, and pinned Flyway `info`/`validate` only; no `migrate`, `repair`, `clean`, reset or destructive tests |

The [image maintainer](https://github.com/gvenzl/oci-oracle-free#supported-tags) currently lists the 23.26.3 image family as supported. Oracle software remains subject to the [Oracle Free Use Terms and Conditions](https://www.oracle.com/downloads/licenses/oracle-free-license.html); the build-script license alone does not establish rights to the database software. The owner must confirm applicable image/license approval. This Free image can provide connection/orchestration smoke evidence only: it does not satisfy the independent Oracle 19c/21c acceptance matrix. Those versions require separately approved images/distributions and entitlement. An approved complete-file manifest and exact Flyway/JRE/JDBC/Oracle-module version combination also remain required; a checksum generated from an unapproved download does not establish provenance.

### Read-only acceptance procedure after approval

1. Recheck the explicit context, permissions, worker/storage capacity and namespace absence. Review the exact pinned image, PVC, resource bounds, Secret references, readiness/startup probes and ClusterIP manifest before applying only the dedicated namespace resources. Do not deploy the manager as part of database preparation.
2. Confirm PVC Bound, actual volume/storage class, pod image ID, ready containers and database health. A running pod or open TCP port alone is insufficient. Do not use a password-printing random-password mode or publish unredacted initialization logs.
3. Read the created Service and EndpointSlice, discover the cluster DNS domain from cluster/pod DNS configuration, and resolve the actual Service name from the approved client location. Record its real namespace, name, ClusterIP, port, DNS result and listener/PDB service. No isolated Service, DNS name, PDB service or endpoint has been discovered yet.
4. Connect using the new execution account through the actual Service route and capture the read-only identity and permission evidence below. Obtain any privileged database identity evidence through a separately approved observer; do not grant DBA merely to make diagnostics pass.

   ```sql
   SELECT banner_full FROM v$version;
   SELECT SYS_CONTEXT('USERENV', 'DB_NAME') AS db_name,
          SYS_CONTEXT('USERENV', 'CON_NAME') AS con_name,
          SYS_CONTEXT('USERENV', 'SERVICE_NAME') AS service_name,
          SYS_CONTEXT('USERENV', 'SESSION_USER') AS session_user,
          SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') AS current_schema
     FROM dual;
   SELECT privilege FROM session_privs ORDER BY privilege;
   SELECT role FROM session_roles ORDER BY role;
   SELECT tablespace_name, bytes, max_bytes FROM user_ts_quotas;
   ```

5. Discover the configured history owner/table through `all_tables` and exact identifiers, then read any existing rows without creating a history table. Report an absent table separately from a permission denial. Capture read-only `info` and `validate` JSON and exits from the approved pinned Flyway runtime using the actual empty/approved resolver files and fixed history settings. Preserve raw sanitized evidence and compare it with the SQL identity/history results. Missing history or insufficient catalog access must remain explicit acceptance gaps, not fabricated success.
6. Inspect effective grants, roles, ownership and quota against the intended execution scope. Metadata can establish grants but does not prove DDL capability or cross-schema isolation; those remain unverified until separately authorized executable acceptance. Do not submit manager jobs: a failed job can trigger its implemented automatic repair policy. Keep real migration, failure/repair and partial-DDL tests disabled.

Provisioning is stopped at the authorization/image gate. The dedicated namespace, PVC, Oracle pod and Service have not been created; no live Oracle/Flyway tests have been performed by this publication task. The existing 19c/21c and operational acceptance gaps remain open.
