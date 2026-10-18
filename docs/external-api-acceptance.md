# Project deployment API acceptance — 2026-10-18

Real HTTP/API integration: **PASS**. Flyway execution: **SIMULATED NATIVE FIXTURE**. Real Oracle execution: **NOT_RUN**.

The acceptance launches the actual embedded Manager binary as an OS process on a loopback port, creates an isolated SQLite installation and encrypted credential through supported HTTP administration, and uses an independent `net/http` client. The published Python client also completes the full workflow from an absent state file and resumes it without another migration. No Manager handler is mocked. The fixture implements the Flyway subprocess protocol; no Oracle database is contacted and no Oracle SQL is executed.

The implementation branch starts at `9975650`, the head of PR #10, which includes current master and guarded mandatory Oracle Repair. It is delivered as a PR based on `fix/oracle-no-automatic-repair`, explicitly dependent on #10. The original `feat/postgresql-foundation` worktree and its uncommitted changes were preserved.

## Recorded deployment

These IDs belong to the isolated acceptance installation, which is removed after the test. They are evidence identifiers, not a live service to query.

| Field | Actual value |
|---|---|
| Project ID | `urneUU5UKth7wK82nYVzRY` |
| Deployment Target ID | `_ktsQyZ8GndKOPnRLwvwN1` |
| Artifact ID | `RhEaB9VBzKT4tDYq751OmL` |
| Original ZIP SHA-256 | `250e5fe1be05c0e1e947c6ba59ae1680360d603ca5774adf4411347d6b2f783c` |
| Preflight ID | `YGKz_-gWrdpzLbc35YjeNG` |
| Run ID | `PPeH_rEZcAIGymNilsU7jv` |
| Preflight result | `ready: true`, `live_status: VERIFIED` under the subprocess fixture |
| Terminal result | `SUCCEEDED`, `completed: true`, `record_available: true` |

[Sanitized actual requests and responses](evidence/external-api-20261018.json) include discovery, multipart upload metadata and inventory, preflight request/assessment, asynchronous `202` with `Location`/`Retry-After`, terminal polling, logs and the frozen execution record. The ZIP contains `V1__native.sql`; its original bytes are persisted in SQLite and its SHA-256 is verified before acceptance.

The main acceptance proves two concurrent upload requests return one Artifact ID and two concurrent submissions return one Run ID. The native migrate command runs once. After renaming and retiring the target, replaying those original keys still returns the original resources; new uploads and runs are denied. Restarting the actual Manager process preserves the artifact checksum/inventory, preflight, run and byte-identical frozen record. Download headers include `ETag`, attachment filename and `Cache-Control: no-store`.

## Authorization and contract negatives

| Actual HTTP check | Status | Result |
|---|---:|---|
| cross project | 404 | PASS |
| cross project artifact | 404 | PASS |
| cross project target list | 404 | PASS |
| legacy token adopted stream bypass | 409 | PASS |
| missing token | 401 | PASS |
| revoked token | 401 | PASS |
| scoped token v1 bypass | 403 | PASS |
| stale revision | 409 | PASS |
| token without execute | 403 | PASS |
| wrong checksum | 422 | PASS |
| wrong project target | 404 | PASS |
| PATCH missing If-Match | 428 | PASS |
| PATCH stale If-Match | 412 | PASS |
| New upload to retired target | 403 | PASS |
| New submission to retired target | 403 | PASS |
| Reuse a log cursor on a different resource | 400 | PASS |

Core tests additionally exercise explicit PROD authority, principal membership and token-scope intersections, scope-preserving token rotation, revocation before guarded Repair, audit rollback, blocked/expired or changed preflights, and bounded keyset pagination. A runtime-unavailable assessment remains `NOT_VERIFIED`, persists its blocker, and never claims readiness.

## Persistence and execution compatibility

Migration 009 adds nine focused tables plus indexes, composite foreign keys and immutability triggers. Migrations 001–008 are unchanged. The v8→v9 test preserves legacy run snapshots, credential ciphertext bytes, original artifact BLOB bytes and audit identity, checks `PRAGMA integrity_check` and `foreign_key_check`, rejects a second Project owner, enforces destination immutability and rejects mismatched artifact/preflight attribution and revision. New attribution is not guessed for historical data. Account rename/update continues to work after adoption; removing its schema mapping disables writes and yields a durable `NOT_VERIFIED` assessment. Restoring the mapping restores readiness. The regression passes under the race detector, and independent review verified the stable account foreign key and live mapping checks.

Unadopted streams retain v1 behavior. Adoption waits for earlier jobs to settle. Adopted streams require the Project Deployment Target API for upload/preflight/submission. Reads of new attributed artifacts/runs enforce Project/target/token policy through both API versions. Retiring or archiving preserves ownership and history; foreign keys prevent deleting referenced Projects or destinations.

The original worker, stream serialization, job claim/recovery, pinned runtime, normalization, secrets store and guarded Oracle Repair paths remain in use. Focused safe/unsafe fixture tests prove original migration outcome remains FAILED, Repair outcome is independent, ambiguous results retain NEEDS_REVIEW/held behavior, and failed SQL is not replayed. Revoking execution authority during a failed migration prevents subsequent Repair.

## Verification run locally

| Gate | Result |
|---|---|
| `make check` | PASS: build, vet, all Go packages, lint and frontend tests |
| `make test-race` | PASS; focused final authorization/HTTP tests also rerun with `-race` |
| `make test-e2e` | PASS: 43 passed; one opt-in PostgreSQL lab test skipped |
| `make test-openapi` | PASS: OpenAPI 3.1 plus ten fresh real HTTP responses and submitted JSON bodies validated |
| `make security` | PASS: npm audit zero vulnerabilities; govulncheck zero reachable vulnerabilities (one module-only advisory reported as uncalled) |
| SQLite migration/integrity and authorization isolation tests | PASS |
| Existing Oracle Repair safety regressions | PASS within Go/race suites; simulated fixtures |
| Published Python client fresh workflow and lost-response replay | PASS against actual Manager subprocess |
| Secret scanning | Gitleaks v8.30.1: no leaks in implementation diffs |
| Independent review | Completed; collection scalability finding fixed with bounded SQL keyset pages and regression coverage |

The GitHub PR runs the same verification workflow, including the new real-process OpenAPI gate. Its current check status is authoritative in GitHub and reported with the delivered PR.

## Live Oracle gate

Real Oracle success, SQL failure/native diagnostics, eligible Repair, ineligible/ambiguous Repair and restart recovery against a disposable approved database remain **NOT_RUN**. They require separate authorization. No production Kubernetes, GitOps, Jenkins, identity, LDAP, Cilium or Elastic configuration was changed. No shared database/schema was created, altered or dropped, and no PR was merged or deployed.

See the [API guide and endpoint inventory](external-api.md), [OpenAPI contract](openapi.yaml), and [generic client](../scripts/external-api-client.py).
