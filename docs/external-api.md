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

Preflights expire after ten minutes and bind the actor, artifact, revision, effective configuration and stream inventory. Live checks still run in the existing worker. A preflight never reserves execution authority. Changed/revoked permissions or credentials can block the worker. No target password or service token is included in returned artifacts, runs, logs or records.

## Verification

Install both `scripts/external-api-requirements.txt` and `scripts/openapi-requirements.txt`, then run `make check test-race test-e2e test-openapi security`. The OpenAPI gate starts a fresh real Manager process, drives it through HTTP with isolated SQLite, and validates actual requests/responses against the specification. Its native Flyway fixture simulates SQL outcomes. Real Oracle SQL execution remains a separate, explicitly authorized acceptance gate.
