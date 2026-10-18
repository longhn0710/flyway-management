# Oracle Jenkins execution readiness

Inspected 2026-10-18 (Asia/Ho_Chi_Minh). No live Oracle SQL, migration,
preflight, repair, deployment, infrastructure change or Jenkins build was run.
Manager authentication and metadata GETs, Kubernetes reads, narrow Jenkins XML
inspection and a controller HTTPS health GET supplied the observations below.
Credential values were neither printed nor committed.

## Mandatory Automatic Oracle Repair

PR #10 has been corrected to retain the pre-PR guarded Automatic Repair workflow.
Automatic assessment after a failed Oracle migrate is mandatory product behavior;
there is no Manager enable/disable setting and Jenkins must not require it disabled.

The original CLI stdout/stderr/JSON and exit code commit before recovery. The worker
publishes `migration_outcome=FAILED`, the original root cause and `repair_outcome=PENDING`,
then assesses Repair. It retains the existing checks: exactly one newly failed,
originally pending versioned migration; unchanged unrelated applied history; no
repeatable/superseded history; validation isolating the same failed version; matching validation filepath when supplied; and
fresh authorization, mapping, credential, snapshot and runtime checks.

A complete pinned native `MigrateErrorResult` (operation, version, success=false,
migrations and error code/message) distinguishes a confirmed migration failure
from a generic CLI/configuration error. Timeout, cancellation, signal-like exit,
truncated/malformed/wrong-version output and an explicit ambiguous result stay
`NEEDS_REVIEW` with Repair `UNSAFE`. Confirmed failure means the CLI failure is
known, not that Oracle DDL rolled back. The runtime's previous broad ambiguous
classification is narrowed for confirmed Oracle failure only; PostgreSQL keeps its
existing lifecycle classification and never enters this Oracle repair branch.
The native error shape was checked against the actual Flyway 13.10.3 classes
and an actual CLI SQL failure using its bundled H2 driver in a process-local
in-memory database. The opt-in TestActualFlywayMigrationFailureEnvelope verifies
that real stdout, native error code and failed path are recognized. This verifies
CLI serialization, not Oracle partial-DDL or Repair acceptance. No real Oracle
failure or Repair was induced for this correction.

Eligible Repair runs once with the same frozen locations. It must remove exactly
the failed version, align/delete nothing else, pass validate and restore the exact
prior resolver view. The final states remain independent:

| Migrate | Repair | Run / stream |
| --- | --- | --- |
| Success, verified history | Not needed (`SKIPPED`) | `SUCCEEDED` |
| Failed | `SUCCEEDED` | `FAILED`; no SQL replay |
| Failed | `FAILED` | `NEEDS_REVIEW`, held |
| Failed | `UNSAFE` | `NEEDS_REVIEW`, held |
| Ambiguous | `UNSAFE` | `NEEDS_REVIEW`, held |

Repair no longer overwrites the run's original failure reason. Optional `failure`
on run/record detail reads exposes the retained migrate execution ID, native
script/version when known, Flyway error code, message and original exit code.
Identity comes only from native error path/message or the first retained failed
history capture; unknown/conflicting identity is explicit. The original JSON and
immutable SQL remain available. Repair's reason, before/after history and execution
reference stay on the existing independent repair record. No database schema
upgrade is introduced. Interrupted pending Oracle assessments close as UNSAFE;
interrupted Repair closes as FAILED, with a held stream and no repair retry.

Jenkins must fail a migration build even when Repair succeeds. The existing demo
poll helper already rejects FAILED, NEEDS_REVIEW and REPAIRING; it only accepts
SUCCEEDED with migration SUCCEEDED and repair SKIPPED. Run the offline regression
against that actual helper without editing the demo repository:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/test-oracle-jenkins-contract.py /path/to/demo-flyway-manager
```

## Observed infrastructure

| Item | Verified observation / limit |
| --- | --- |
| Demo source | [Existing PR #1](https://github.com/devsecopslonghn/demo-flyway-manager/pull/1) is merged; reviewed feature head `a2ae95d4b6a960d51afc517c3cd465c95a698cd7`. Existing Jenkinsfile and source were not recreated or modified. |
| Jenkins | `https://jenkins.apps.drgdevlab.com`, controller `jenkins-0` Ready, image `jenkins/jenkins:2.568.3-jdk21`. Anonymous management APIs return 403. |
| Plugins | Installed: Declarative Pipeline (`pipeline-model-definition` 2.2293.v6e7193cec599), Git 5.10.1, Credentials Binding 728.v902a_273b_8947, Pipeline Input Step 560.v56198a_642157, Timestamper 1.30. |
| Trusted SCM job | No reference to `devsecopslonghn/demo-flyway-manager` in the 22 inspected stored job configurations. A protected Pipeline-from-SCM job for this source is still required. |
| Agent | Jenkinsfile requires `oracle-cicd-source`; absent from inspected node/job configuration. Two static nodes have `oracal` labels. Online status could not be established (computer API 403); no separate agent pod was present. |
| Credentials | Required IDs: `flyway-manager-{develop,sit,uat,staging}-config` (Secret file), `flyway-manager-{develop,sit,uat,staging}-token` (Secret text), and a site-selected SCM read credential. Required Manager IDs were absent from the inspected global store; folder/job scope still requires administrator confirmation. |
| Manager | `https://flyway-manager.apps.drgdevlab.com`, Ready 1/1. Health reports database OK, SQLite schema 8 and migration execution enabled. API contract is OpenAPI 0.6.0 under `/api/v1`; the correction keeps routes/status semantics and adds optional failure detail. |
| Deployed image | `ghcr.io/longhn0710/flyway-manager@sha256:1a0c80c1ef869a6f1735cdf7bbae9973ffd607c061a07966f2ee621c2690f8d5`. Existing deployed-acceptance evidence correlates this digest to source `f507dcaa543f10cddf2805f06b8169d83fd980d7`, GitOps `9289df29f4f21c160d26539a60579b357702bd5a`. Health does not expose a source revision; reconfirm release provenance before execution. This build has the original guarded Automatic Repair; the reporting/ambiguity correction in PR #10 is not deployed. |
| Runtime | Authenticated `GET /api/v1/runtime`: ready, integrity verified, Flyway 13.10.3, bundled Java/Oracle JDBC available, no blockers. Container filename inventory includes `ojdbc11-21.18.0.0.jar` and `flyway-manifest.sha256`. Source manifest pin: `a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c`. Operator approval/provenance of the exact deployed distribution remains a gate. |
| Oracle candidate | Active DEV target ID `oracle-free-lab`, service `oracle-free.flyway-manager-oracle-lab.svc.cluster.local:1521/FREEPDB1`. Active schema `FWM_WZ260_A`, schema ID `kEgO5_EYblHWxJTS8GmQo6`, history `FWM_WZ260_A.flyway_schema_history`. Default account ID `iwPGnKIas4lDW1-d57HJfj`, username `FWM_WZ260_A`, active, mapped, credential configured and not revoked. Registry metadata confirms a lab candidate, not DBA approval, empty schema, database identity or effective Oracle privileges. |
| Other Oracle | Separate UAT target `oracle-free-main`, schema `FWM_LAB`, exists. Do not substitute it for the DEV lab. Oracle Free pods run in both lab namespaces. No database probe was executed. |
| HTTPS | Jenkins controller GET to Manager `/api/v1/health` returned 200 with TLS verification result 0. Actual build-agent connectivity and CA trust remain unverified. |

The demo's existing `config/manager-policy.json` still blocks EXECUTE using the
superseded requirement that Oracle Automatic Repair must be disabled. This report
does not change the demo repository or bypass its gate. Its policy needs a separate
review to accept mandatory guarded Automatic Repair and the approved Manager
revision. Do not add a Manager toggle or require Jenkins to disable Repair. No
Jenkins/Oracle provisioning is authorized by this report.

## Exact gated execution checklist

All steps below are future work. Stop on missing approval, failed verification,
`NEEDS_REVIEW`, held stream, unknown outcome or changed evidence. The Manager
worker alone performs the guarded Automatic Repair assessment. Jenkins/operators
must not add repair, clean, baseline, undo, hold-clear or SQL replay steps.

1. Review the focused safety PR and its CI. Obtain separate merge and deployment
   authorization, then use the approved release process. Record merged revision,
   immutable image digest, healthy rollout and pinned runtime/JDBC provenance.
   Confirm the running image includes the corrected guarded Repair and failure
   reporting. Review the additive API field against the demo client and separately
   align its policy to accept mandatory Automatic Repair. Preserve existing gates
   until that review is complete.
2. Jenkins administrator provisions or identifies a protected Pipeline-from-SCM
   job for `https://github.com/devsecopslonghn/demo-flyway-manager`, trusted
   `master` commit, Script Path `Jenkinsfile`. Restrict job configuration, SCM
   changes, credential use and approvers. Use a dedicated online Linux agent
   labeled `oracle-cicd-source`, with Bash, Git, Python >=3.10 and `sha256sum`.
   Confirm the installed plugin versions remain available.
3. Administrator binds `flyway-manager-develop-config` (Secret file) and
   `flyway-manager-develop-token` (Secret text), plus the SCM read credential.
   Config must name HTTPS Manager origin, environment `develop`, Manager tag
   `DEV`, exact approved target/schema/account IDs, uppercase schema name and
   actual Jenkins DBA approvers. Use the candidate IDs above only after DBA
   approval. The bearer identity must have active schema-scoped operation rights.
   Do not export Oracle passwords to Jenkins. Other environment IDs are required
   only when those environments are explicitly approved.
4. DBA confirms the selected existing schema is isolated, suitable for the demo's
   object names and V1/V2/V3 versions, has no conflicting historical reservations,
   and has the required Oracle privileges. Confirm PDB, account identity,
   same-schema history and absence of external writers. Inspect held streams and
   earlier failed/uncertain runs; reconcile them under separate authority.
5. From the actual selected Jenkins agent, run a TLS-verified HTTPS GET to
   `https://flyway-manager.apps.drgdevlab.com/api/v1/health` using its approved CA.
   Require HTTP 200; then inspect target, schema, mapped accounts and runtime with
   authenticated GETs. Never print tokens or use an insecure TLS override.
6. Run the trusted job with `ENVIRONMENT=develop`, `MODE=VALIDATE`,
   `AUTHORIZE_MANAGER_ACCESS=false`, `AUTHORIZE_EXECUTION=false`. Require passing
   structure/unit checks and archive resolved manifest, effective SQL-only ZIP
   and ZIP SHA-256. Expect eight effective SQL files, with the develop repeatable
   notification-policy override. Confirm packaged commit and clean source.
7. Obtain Manager-access authorization. Run `MODE=PREFLIGHT`,
   `AUTHORIZE_MANAGER_ACCESS=true`, `AUTHORIZE_EXECUTION=false`. This uploads an
   artifact and runs live info/validate; it is not a migration. Require
   `ready=true`, `local_ready=true`, `live_status=VERIFIED`, no blockers. Review
   `mapping.json`, artifact response/digest, full preflight file set (including
   historical stream files), target/PDB/schema/account and effective SQL bytes.
8. Obtain explicit authorization for the first database execution. Run
   `MODE=EXECUTE`, both authorization booleans true, against the same approved
   source and target. Compare that build's artifact/manifest/preflight with the
   reviewed evidence. A permitted DBA must approve its Jenkins input with
   `CONFIRM=true`; the pipeline re-preflights after approval. Record approver,
   build URL, commit, exact request, idempotency key and Manager run ID.
9. Require terminal `SUCCEEDED`, `migration_outcome=SUCCEEDED`,
   `repair_outcome=SKIPPED`; archive the complete run record, logs, executions,
   files and original submission intent. Confirm no recovery command occurred.
   Independently read Oracle history and `USER_OBJECTS`/`USER_ERRORS`: V1/V2/V3
   and all five repeatables successful; CUSTOMER, APP_CONFIGURATION, CUSTOMER_SEQ,
   IX_CUSTOMER_STATUS, CUSTOMER_LABEL, UPSERT_CUSTOMER, ACTIVE_CUSTOMERS,
   CUSTOMER_NOTIFICATION_POLICY and CUSTOMER_TIMESTAMPS exist and compile.
   Verify seed/config rows and develop `CAPTURE_ONLY` policy. These are read-only
   checks; calling the upsert procedure would be an additional write.
10. With explicit authorization for the no-op proof, run an identical committed
    artifact in a new build. Require native `migrationsExecuted=0`, unchanged
    versioned/repeatable history and unchanged objects/data. Separately verify
    replay of the original identical actor/key/request returns the original run
    ID, without another migrate. Do not change the request under the same key.
11. Review and commit one change to the **effective** develop repeatable
    `environment/develop/repeatable/R__40_notification_policy.sql`; retain its
    filename/path and all V migration bytes. Repeat VALIDATE, authorized PREFLIGHT,
    artifact review and explicit EXECUTE/DBA approval. Require exactly that
    repeatable's real re-execution, a new checksum/history entry and observable
    approved view change; V1/V2/V3 must remain unreplayed. Archive before/after
    history and results.

If a migration fails, Jenkins must report FAILURE even if Repair succeeds. Wait
for and collect the final independent Repair evidence from Manager; a Jenkins
failure during PENDING/REPAIRING does not cancel Manager recovery. An unsafe or
uncertain outcome blocks queued successors and holds the stream. Repair never
rolls back Oracle DDL or replays SQL.

On uncertain POST transport or polling failure, use archived
`reports/manager/submission-intent.json` and the original key to reconcile Manager
state. A failed Jenkins build does not cancel a running Manager job or prove
rollback. Manual recovery and every further database write require separate
authorization. Never infer approval from elapsed time or a healthy runtime.
