# Project deployment API (v2)

The `/api/v2` API lets Jenkins, GitLab, or another authenticated caller submit SQL artifacts through a project and an explicitly configured deployment target. A deployment target binds one project to an existing Manager database target, schema, and execution account. Environment and destination are selected in Manager configuration; clients never send credentials or arbitrary SQL commands.

JSON API success responses use a `data` property; the execution-record download returns the frozen JSON document directly. Collection responses also have `page.limit` and may include `page.next_cursor`. Errors use `application/problem+json` with RFC 9457 fields (`type`, `title`, `status`, `detail`, `instance`) and Manager `code` / `field` extensions. JSON request bodies reject unknown properties and are limited to 1 MiB. Collection cursors use immutable IDs in ascending order and bounded SQL batches. Cursors are signed, caller and exact resource bound; use the returned cursor unchanged. Cursors expire on Manager restart; start the collection again. Logs are append-only and use an offset cursor. The first page defaults to 50 rows and `limit` may be 1 through 200.

## Routes

| Method and path | Purpose |
|---|---|
| `GET /projects`, `GET /projects/{projectID}` | List visible projects or read one project. |
| `GET /projects/{projectID}/deployment-targets` and `GET .../{deploymentTargetID}` | Read project deployment mappings. |
| `GET .../{deploymentTargetID}/grants` | Administrator reads configured grants. |
| `POST /projects/{projectID}/deployment-targets` | Administrator creates a mapping. |
| `PATCH /projects/{projectID}/deployment-targets/{deploymentTargetID}` | Administrator changes its name or active state and advances the revision using `If-Match: "<revision>"`. Project and destination identity are immutable. Missing preconditions return `428`; stale ones return `412`. |
| `PUT /projects/{projectID}/deployment-targets/{deploymentTargetID}/grants` | Administrator sets the specified user/service principal's project-target actions. |
| `POST /projects/{projectID}/deployment-targets/{deploymentTargetID}/artifacts` | Upload exactly one ZIP with `files`, `deployment_target_revision`, `input_sha256`, optional JSON `external_source`; requires `Idempotency-Key`. |
| `GET /projects/{projectID}/artifacts/{artifactID}` | Read project-bound artifact metadata. |
| `POST /projects/{projectID}/deployment-targets/{deploymentTargetID}/preflights` | Persist and return a read-only deployment assessment. Body has `artifact_id`, `deployment_target_revision`, optional `external_source`. |
| `GET /projects/{projectID}/preflights/{preflightID}` | Read a preflight owned by this project. |
| `POST /projects/{projectID}/deployment-targets/{deploymentTargetID}/migration-runs` | Submit a checked run asynchronously; requires `Idempotency-Key`, returns `202`, `Location`, and `Retry-After`. |
| `GET /projects/{projectID}/migration-runs`, `GET .../{runID}` | List or inspect project-attributed runs. |
| `GET /projects/{projectID}/migration-runs/{runID}/logs` | Page through run logs. |
| `GET /projects/{projectID}/migration-runs/{runID}/record` | Download the finalized immutable JSON execution record with an `ETag`. |

Artifact and run idempotency keys must be stable across retries. Reusing a key with a different request returns a conflict. A `202` means the run was accepted, not that Flyway finished. Poll the run resource until its `completed` field is true; inspect `status` independently because a failed run may still be pending guarded Oracle Repair. A client timeout is not a reason to cancel or submit under a new key.

`external_source` records caller-reported CI metadata (`system`, `execution_id`, `execution_url`, `workflow`, `repository_url`, `revision`). It is descriptive provenance, not cryptographic proof that the source ran in that CI system. Manager validates the configured destination and current grants again at execution time. Failed migrations stay failed; the guarded Oracle Repair path can only restore known history state and never replays the failed SQL.

## Generic Python client

Requires Python 3.9+ and `python3 -m pip install -r scripts/external-api-requirements.txt`. The client below discovers a project and target by their unique names, uploads a local ZIP with its SHA-256 digest, runs preflight, submits once using a caller-provided stable key, polls, then retrieves paginated logs and the finalized record. Keep `--key` unchanged when retrying the command after a network timeout. The client stores the request identity, artifact/preflight IDs, and accepted run ID in a mode-0600 state file named from the key (or in `--state-file`); it never stores the service token. It reuses the exact preflight request after a lost response. If that preflight expires before any run was accepted, start a new release with a new key and fresh assessment.

```sh
python3 scripts/external-api-client.py \
  --url https://manager.example \
  --project payments \
  --deployment-target production \
  --zip ./release.zip \
  --key "gitlab:payments:release-2026-10-18" \
  --external-source '{"system":"GitLab","execution_id":"918273","workflow":"release","revision":"abc123"}'
```

The token is a Manager service token with explicit project membership, deployment-target grants, existing database/schema grants, and token deployment scopes. Set `deployment_scopes` when issuing or rotating the token through the existing service-token API; each scope has `deployment_target_id` plus `read`, `upload`, `preflight`, and `execute` booleans. Existing v1 tokens without those scopes have no v2 access, and a scoped token cannot mutate v1 routes (attributed v1 reads still enforce project/target scopes). Provide the token through a protected CI secret. Do not print it in job logs. Jenkins and GitLab jobs can invoke this same client; only `--key` and `--external-source` need pipeline-specific values.

## Configuration and compatibility

An Admin creates a Deployment Target under Project Details, selecting an existing database target, schema and mapped account. Creation explicitly adopts that database/schema stream; one Project can own it, and adoption waits for pending jobs to settle. Multiple Deployment Targets in the same environment are allowed. The destination tuple is immutable. Rename or retire a target using its current revision and `If-Match`; retirement preserves history and ownership.

Grant a user or service principal target actions in the target editor. This establishes its Project identity membership and target grant; existing database/schema grants remain required, including explicit PROD authority. Issue a token with a subset of target actions in Service Accounts. Token rotation without a replacement scope list preserves the original scopes. Legacy tokens receive no implicit v2 access.

After adoption, v1 upload/preflight/submit writes to that stream return `409 PROJECT_TARGET_REQUIRED`. Scoped tokens also cannot mutate v1 routes. Existing unadopted streams retain their v1 behavior. Legacy artifacts/runs remain unassigned; their bytes, historical identities, credentials, and audit rows are not rewritten or guessed into Projects. The UI labels their membership-based grouping as Legacy history.

Migration `009_project_deployments.sql` is additive; migrations 001–008 retain their checksums. Project ownership, artifact/run attribution, preflights and finalized records cannot be deleted through this API. Foreign keys restrict deleting a referenced Project or destination. Archive or retire it to preserve evidence. Back up SQLite and the original encryption master key together before a future rollout; an older binary must not open schema 9.

Preflights expire after ten minutes and bind the actor, artifact, revision, effective configuration and stream inventory. Live checks still run in the existing worker. A preflight never reserves execution authority. Changed/revoked permissions, account mappings or credentials can block the worker. Account administration retains its existing update behavior; removing a mapping disables upload/execution while read-only preflight can persist a blocked assessment. No target password or service token is included in returned artifacts, runs, logs or records.

## Verification

Install both `scripts/external-api-requirements.txt` and `scripts/openapi-requirements.txt`, then run `make check test-race test-e2e test-openapi security`. The OpenAPI gate starts a fresh real Manager process, drives it through HTTP with isolated SQLite, and validates actual requests/responses against the specification. Its native Flyway fixture simulates SQL outcomes. Real Oracle SQL execution remains a separate, explicitly authorized acceptance gate.

## Oracle failed migration correction

SQLite migration 010 adds one single-use approval table; migrations 001–009,
encrypted credentials and existing immutable records are unchanged.

A confirmed Oracle failure automatically receives one guarded Repair assessment.
Successful historical repeatables do not disqualify it. Before Repair, Manager
requires exactly one confirmed failed version, native validation isolating that
failure, and stable physical checksum-bearing history. Earlier successes in the
same batch must match frozen SQL and native migrate's successful-file list.
After Repair, native output must report only that failed version removed with no
alignment/deletion, physical successful rows must be identical, and info/validate
must restore the resolver view with the failed version and successors Pending.
The original migration stays FAILED and the original Jenkins build stays FAILURE.
The stream stays held. Repair never rolls back SQL or retries it.

The execution account must support the pinned JDBC probe's fixed read-only
queries: `V$DATABASE` DBID/DB_UNIQUE_NAME, authenticated session identity,
`ALL_OBJECTS`, `ALL_TABLES`, and the configured Flyway history table. The primary
path uses the schema's own account. Insufficient metadata privilege or unavailable
probe evidence makes Repair UNSAFE and keeps the stream held; Manager grants
nothing. No broad dictionary role is required by the implementation. Existing
v1/v2 endpoints and standard client payloads remain supported. A legacy successful
Repair without physical evidence cannot authorize a correction retroactively.

Correction review and approval use the same bounded **50-second whole-operation**
budget as native preflight, below the HTTP server's 60-second write deadline.
Clients should allow at least 60 seconds for the response. An expired operation
returns HTTP 504 `REQUEST_TIMEOUT` (cancellation: 408 `REQUEST_CANCELLED`);
unavailable or unverifiable Oracle identity/history evidence returns 409
`ORACLE_EVIDENCE_UNAVAILABLE` (insufficient metadata privilege is one cause).
Mandatory audit failure returns 500 `AUDIT_FAILURE`. Failure auditing has a
separate bounded two-second context; verification and successful review/approval
writes remain request-bound and atomically audited. Timeout or audit failure
never yields a VERIFIED review or an approval. Preserve the response/request ID
and reconcile state before another attempt; do not retry migration SQL.

After inspecting native failure/logs and independently handling any partial DDL,
a human Admin or Operator with current Project/target/database execute authority
uploads the candidate corrected ZIP through the ordinary v2 artifact endpoint.
It must contain every file from the failed run's frozen locations, including V7
and repeatables. Exactly the failed version's bytes may differ; version/path and
all other bytes must be unchanged. No arbitrary hold-clear or Repair endpoint is
added. The failed artifact, SQL, run and frozen record remain immutable.

With normal session/CSRF headers, request a read-only review:

```http
POST /api/v2/projects/{projectID}/deployment-targets/{deploymentTargetID}/correction-reviews
Content-Type: application/json

{"failed_run_id":"<original-failed-run>","artifact_id":"<corrected-candidate>","deployment_target_revision":1}
```

The HTTP 200 response is a **review-only** preflight, retrievable through the
existing preflight GET. Require ready=true, live_status=VERIFIED and blockers=[].
It includes current physical history, database identity and an object-catalogue
fingerprint. It is not an executable preflight. After inspecting the exact SQL
and current state, approve the original ZIP checksum:

```http
POST /api/v2/projects/{projectID}/deployment-targets/{deploymentTargetID}/correction-approvals
Content-Type: application/json

{"preflight_id":"<review-id>","input_sha256":"<corrected-ZIP-SHA256>","partial_ddl_reviewed":true,"note":"DBA reviewed native failure and partial DDL; current schema is ready for this exact correction."}
```

HTTP 201 returns a single-use approval expiring with the review (at most ten
minutes). Service accounts cannot review or approve. Approval rechecks current
Oracle evidence and destination/authority; it does not release the hold or run SQL.
The developer may then push the exact reviewed bytes and Jenkins may upload that
same ZIP under a new Artifact ID, using its existing scoped token and approval
controls. The ordinary preflight → HTTP 202 submission → poll → logs/record client
contract is unchanged. Approval matches the ZIP checksum within the exact Project
and Deployment Target, not the uploader or Artifact ID. It is consumed atomically
by one Run ID and is rechecked against the worker's live evidence and current DBA
permissions before migration. Changed database/history/catalogue, mappings,
credentials, target revision, runtime, expired authority or another migration
attempt requires a new DBA review. A verified successful corrected V6 → V7 run,
including unchanged prior physical
history, releases the hold. Its frozen preflight retains the approval ID, DBA and
before/after physical evidence. V1–V5 and unchanged repeatables are not executed
again.

Repair FAILED/UNSAFE/unknown outcomes remain NEEDS_REVIEW and held. There is no
automatic Repair retry, SQL retry, history editor or failed-Repair remediation
workflow. A restart holds interrupted work and never replays a consumed attempt.
Object metadata and history are evidence, not a snapshot of all application data;
the DBA's partial-DDL/data inspection remains explicit. Operationally exclude
external schema writers during review/submission/execution; Manager stream locks
do not lock external Oracle writers.

Local coverage: `TestOracleV1V7CorrectionThroughHTTPAndPublishedCIClient` exercises
local Git commits/pushes to a disposable bare repository,
the actual HTTP handlers and published Python client against a stateful native
protocol fixture, with five prior repeatables, unchanged successful history, failed V6,
pending V7, verified Repair, held stream, DBA review and one corrected execution.
A second fixture covers V1–V5 committing earlier in the failing V1–V7 batch.
Guard tests cover stale evidence, unauthorized approval, unrelated file changes,
consumption, expiry, worker recheck, failed Repair and hidden checksum realignment.
These are simulated Oracle fixtures; no live Oracle or Jenkins execution is
claimed by these tests. The real Jenkins reference helper also has six offline
completion/Repair-outcome contract checks. Run them against an authorized local
demo checkout with `python3 scripts/test-oracle-jenkins-contract.py /path/to/demo`.
The HTTP fixture/client tests run in the ordinary Go/CI suite; CI does not fetch
the private demo repository or require additional credentials.
