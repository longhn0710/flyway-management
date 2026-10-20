# Oracle correction release and CASE 3 readiness

Reviewed 2026-10-19 (Asia/Ho_Chi_Minh). **Existing FWM_WZ260_B CASE 3: BLOCKED.**
This procedure is prepared for review; it authorizes no rollout, privilege grant,
Repair, corrected SQL submission or new intentional failure. Historical Jenkins
and Oracle acceptance remains in the [demo report](https://github.com/devsecopslonghn/demo-flyway-manager/blob/84b6a84044d3cd8b857c4d3be6a8170dd6208608/docs/git-to-oracle-demo.md).
This replaces the superseded 2026-10-18 readiness observations.

## Verified release and lab boundary

| Item | Verified value / limit |
| --- | --- |
| PR #12 | Merged by repository owner on 2026-10-19; reviewed head `3bbe8219cd19b8340618a8e581edf6ffc23f5788`, merge `4c54fb998b2441065ec39b55c79a43817ad554b5`. The agent did not merge it. PR verification passed. |
| Current application | Health HTTP 200, database OK, installation `08c3533660b75625462d3ab3dd1f7071`, schema **9**. |
| Current deployment | One replica, `Recreate`, `ghcr.io/longhn0710/flyway-manager@sha256:16e7438614f48b6f89961963a7054bf047c823bf47bc43b353c25c68e4eff768`. PR #12 is not deployed. |
| Project / Deployment Target | `vxfQISv7e-d1Wjhyt1HBE9` / `9E4T5dm5YZPhNMEBNk1Zds`; recorded revision 1, recheck before future use. |
| Registry destination | Target `oracle-free-lab`; schema ID `9Uj-JmnInf5RlfvXKenZbZ`; account ID `3yqtQcwm7MtcPdmhq4AU68`. |
| Oracle destination | `oracle-free.flyway-manager-oracle-lab.svc.cluster.local:1521/FREEPDB1`, TCP; DBID `1514744704`, DB_UNIQUE_NAME `FREE`; schema/account `FWM_WZ260_B`. |
| Original CASE 2 | Run `Dn45dSyyZbrXzR-JhnScYi`, Jenkins [EXECUTE #11](https://jenkins.apps.drgdevlab.com/job/devsecopslonghn/job/demo-flyway-manager/job/demo%252Fgit-to-oracle-20261019/11/) FAILURE; retained Manager result NEEDS_REVIEW / migration FAILED / Repair UNSAFE, ORA-00957. No native Repair ran. |
| Immutable original record | SHA-256 `15c632a38bd7bb68e336bdcb4a05917e553cd7c9b7c029ffd3ca9083f0ab72c5`; compare raw authenticated record bytes and ETag after any future rollout. |
| Read-only Oracle observation | Nine Success rows: V1, V2, V3, V5 and five repeatables. One Failed V6 at rank 10, CRC `-224398153`; no V4 or V7 history. No `FWM_CICD_V6_INVALID` table; zero invalid B objects. |
| Privilege observation | Exactly CREATE SESSION, CREATE TABLE, CREATE SEQUENCE, CREATE PROCEDURE, CREATE VIEW, CREATE TRIGGER; no roles; USERS quota 8 MiB. No direct B or PUBLIC grant on SYS.V_$DATABASE. No password/quota/grant changes were made. |

The metadata observations used an existing OS-authenticated DBA session and only
SELECT, a session-local container selection, a read-only transaction and ROLLBACK.
The identity and physical-history query syntax (including quoted lowercase
history columns and FF9 timestamps) ran successfully on this Oracle instance.
This does **not** prove that the B account can run the new probe: its V$DATABASE
access is currently missing. No Jenkins build, Manager preflight, migration or
Repair was triggered for this review.

## Product flow and review results

For a new eligible failure observed by this release, Manager retains the native
migrate error and original SQL/run first, then assesses Automatic Repair once.
Historical successful repeatables, including unchanged Superseded applications,
are supported. Before Repair, the failed row and any earlier successes in the
same batch must match frozen SQL/native results and physical CRCs. After Repair,
all successful physical history fields must be unchanged; native Repair must
report only the one failed version removed, no alignment/deletion, and subsequent
info/validate must restore its Pending state. Repair never rolls back Oracle DDL.

A verified Repair leaves migration FAILED, CI FAILURE and the stream **held**.
A human Admin/Operator with current Project/target/database execute permission
reviews current state and approves an exact corrected ZIP. Only failed V6's
bytes may change; its name/version and every other frozen file must be retained.
The worker rechecks permissions, identity/history/catalogue, destination,
credentials, runtime and expiry before migration. It executes corrected V6 then
already-frozen pending V7, verifies prior successful physical rows, and only then
releases the hold. The original failed record and SQL remain immutable. Users do
not operate Flyway CLI or edit Flyway history.

SQLite 010 adds one approval table, indexes and immutability triggers. It leaves
migrations 001–009 unchanged. Admission and single-use consumption commit in one
immediate transaction with one SQLite connection. Competing submissions with
different keys while held admit exactly one approved run; the loser is rejected.
Same actor/key/request replay returns the original Run ID. After a completed
correction releases the hold, normal v2 no-op submissions retain existing semantics.
Approval creation does not release the hold. Role revocation, stale evidence and
expired/consumed approvals fail closed. A consumed attempt is not reusable after
cancellation, failure or restart. Approval expires with the review after at most
ten minutes; the worker must start within that window.

Focused tests additionally cover populated v9 backup → isolated v10 restore,
byte-preserved registry/attribution/artifacts/failed run/record/audit/holds,
original-key credential decryption, integrity/foreign-key checks, unchanged source
backup and rejection by a v9 binary. There is no downgrade migration. Existing
v1 unadopted streams and v2 client payloads remain supported; adopted streams
continue requiring v2. The review found and fixed unqualified dictionary references in the evidence
probe: own-schema views can shadow PUBLIC synonyms. Metadata queries now use
SYS.V_$DATABASE, SYS.DUAL, SYS.ALL_OBJECTS and SYS.ALL_TABLES, with a rebuilt
embedded helper and regression coverage for forged identity and denied access.
No SQLite/concurrency/approval-consumption defect was found. These recovery execution tests use simulated Oracle fixtures.

## Minimum Oracle metadata privilege

The primary path uses B's own account and own history schema. All dictionary
objects are explicitly SYS-qualified to prevent private schema views from
[shadowing public synonyms](https://docs.oracle.com/en/database/oracle/oracle-database/19/sqlrf/CREATE-SYNONYM.html). The JDBC probe
issues only fixed SELECTs after `SET TRANSACTION READ ONLY`, then rolls back.

| Query | Required access for the existing lab |
| --- | --- |
| V$DATABASE DBID / DB_UNIQUE_NAME | One object-level grant on underlying **SYS.V_$DATABASE**; currently absent. |
| DUAL / SYS_CONTEXT USERENV | Existing PUBLIC SELECT on SYS.DUAL and built-in session context. No new grant. |
| ALL_OBJECTS / ALL_TABLES | Existing PUBLIC READ grants were verified; own-schema rows are visible. No new grant. |
| B.`"flyway_schema_history"` | B owns the table. No extra SELECT or DML grant needed. |
| Read-only transaction | Existing CREATE SESSION; no transaction/system dictionary privilege is added. |

Separately authorized DBA proposal, **NOT EXECUTED**, scoped to the existing
local user after checking DBID and selecting FREEPDB1:

```sql
GRANT SELECT ON SYS.V_$DATABASE TO FWM_WZ260_B CONTAINER=CURRENT;
```

Do not grant DBA, SELECT ANY DICTIONARY, SELECT_CATALOG_ROLE, ANY TABLE privileges,
PUBLIC access, common/container-all grants, or further schema privileges. Preserve
the password, quota and existing six grants. This single object SELECT is the
additional access required by the current probe; unrelated Oracle/Flyway features
may require other privileges and are outside this acceptance.

Oracle documents the [underlying V_$ grant](https://docs.oracle.com/en/industries/communications/billing-revenue/15.0/ece-install-guide/ece-preinstallation-tasks1.html),
[container-local grant form](https://docs.oracle.com/en-us/iaas/database-migration/doc/additonal-configurations-preparing-source-database-online-migration.html),
and current-user visibility of [ALL_OBJECTS](https://docs.oracle.com/en/database/oracle/oracle-database/18/refrn/ALL_OBJECTS.html)
and [ALL_TABLES](https://docs.oracle.com/en/database/oracle/oracle-database/18/refrn/ALL_TABLES.html).
Only the narrow object grant above is proposed, not those guides' broader setup.

After separate grant authorization, verify as **FWM_WZ260_B**, not merely SYS,
using the protected existing password without printing it:

```sql
SET TRANSACTION READ ONLY;
SELECT DBID, DB_UNIQUE_NAME FROM SYS.V_$DATABASE;
SELECT SYS_CONTEXT('USERENV','CON_NAME'), SYS_CONTEXT('USERENV','SERVICE_NAME'),
       SYS_CONTEXT('USERENV','SESSION_USER'), SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM SYS.DUAL;
SELECT COUNT(*) FROM SYS.ALL_OBJECTS WHERE OWNER='FWM_WZ260_B';
SELECT COUNT(*) FROM SYS.ALL_TABLES WHERE OWNER='FWM_WZ260_B' AND TABLE_NAME='flyway_schema_history';
SELECT "installed_rank", "version", "description", "type", "script", "checksum",
       "installed_by", TO_CHAR("installed_on",'YYYY-MM-DD"T"HH24:MI:SS.FF9',
       'NLS_DATE_LANGUAGE=English NLS_CALENDAR=Gregorian'), "execution_time", "success"
  FROM "FWM_WZ260_B"."flyway_schema_history" ORDER BY "installed_rank";
ROLLBACK;
```

Require DBID 1514744704, FREE / FREEPDB1, account/current schema B, exact successful
history and the expected failed V6. Verify the fixed ALL_OBJECTS catalogue query
from `internal/oracle/OracleEvidenceProbe.java` as B too. Any denied or ambiguous
metadata query blocks recovery; do not compensate with a broad role.

## Safe rollout procedure — separate authorization required

1. Record the exact approved source and successful verification/publication run.
   **Do not promote the uncorrected PR #12 merge image**
   (`4c54fb998b2441065ec39b55c79a43817ad554b5`): it lacks the SYS-qualification
   fix. The focused follow-up must pass CI and receive separate merge/release
   authorization first. Its future merge revision determines the CI image tag
   `ghcr.io/longhn0710/flyway-manager:phase4-<approved-merge-sha>`.
   A fixed release digest is NOT_PUBLISHED by this PR-only review. Resolve and retain the successful publisher's immutable digest and matching
   OCI revision. **Never deploy by tag or guess a digest while publication is
   pending.** A later code fix requires its own reviewed revision/digest and CI.
2. Pause external submitters through the existing operational controls; wait for
   no QUEUED/PREFLIGHT/RUNNING/REPAIRING or claimed jobs. Preserve the B hold. Do
   not restart in-flight work, clear holds, or change token/Oracle credentials.
3. Follow the [quiet-window backup commands](homelab-operations.md#backup): use
   the existing Manager backup CLI (`VACUUM INTO`), export the consistent DB and
   original encryption key separately, retain checksums and installation/source
   identity. No source WAL/SHM deletion or hot SQLite file copy. This review did
   not take a production backup.
4. Rehearse on disposable DB/key copies using the **exact candidate digest**,
   `--network none`, no published port, an explicit external original key, and
   no Flyway runtime configuration. See [isolated restore](homelab-operations.md#isolated-restore-rehearsal).
   Require schema 10, unchanged installation ID, registry, credential ciphertext,
   SQL/ZIP bytes, failed run, frozen records/audit and held state. Require all
   saved secrets decrypt, integrity_check=ok and foreign_key_check empty. No
   Oracle connection, job submission or live PVC mount in the rehearsal.
5. Review a digest-only GitOps change to `flyway-manager/values.yaml`; retain one
   replica, Recreate, PVC/key, network policies and runtime manifest pin
   `a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c`.
   After explicit rollout authorization, use the existing GitOps workflow. Never
   patch the running Deployment as a shortcut or change Jenkins/infrastructure.
6. Read actual image/imageID and OCI provenance, Ready 1/1, health schema 10 and
   installation ID. Verify v1 reads/legacy behavior, v2 identity/scopes, runtime
   13.10.3 readiness/integrity and unchanged original CASE 1/2 records. B must
   remain held with its original UNSAFE assessment. A health-200 response alone
   does not establish correction readiness.
7. Separately review the demo's `config/manager-policy.json`: it currently pins
   schema 9 / revision 185581f / old digest. The protected Jenkins config must
   attest to the same newly deployed revision/digest, schema 10, existing
   installation/Project/target/schema/account. Preserve mandatory Repair and
   every approval control; do not weaken verification to accept an unknown build.
   The published client is unchanged: SHA-256
   `324ff4a26397ff7771e3c84ce1e5c88f59fcacc402da5c395afbc1f83187c7a1`.
   Changes to Jenkins configuration/demo release pins require their own approval;
   no Jenkins changes were made here.
8. Rollback: a v9 binary must reject schema 10. Do not point it at the upgraded
   DB. Any authorized rollback needs the matching pre-upgrade DB/key and a
   reconciled post-backup change window. It cannot roll back Oracle SQL, erase
   accepted runs or rewrite frozen records. Keep the original backups/evidence.

The historical Java 25 / OJDBC 21.18 vendor-support exception remains a homelab
exception, not a production support claim. No new runtime or infrastructure is
introduced here.

## Existing B CASE 3 admission gate — currently STOP

Deployment and the narrow metadata grant alone cannot make this existing failure
eligible. `correctionFailure` requires a settled migration FAILED with run FAILED
and Repair SUCCEEDED; `ReviewOracleCorrection` requires verified post-Repair
physical evidence. Original run Dn45dSyyZbrXzR-JhnScYi has neither. Startup and
preflight do not retry Repair or manufacture this evidence. Even a DBA repairing
history outside Manager would not supply the required immutable in-product
post-Repair evidence. Do not try that workaround or edit SQLite/Flyway history.

There is no supported no-CLI primary-path continuation for this historical
UNSAFE run in PR #12. Report **BLOCKED before upload/preflight/approval/SQL** and
retain the failed row/hold until a separately reviewed and explicitly authorized
reconciliation path exists. This review intentionally adds no failed-Repair
recovery feature and authorizes no live Repair or intentional failure rerun.

The original ten-file package also had no V7 (and B has no V4). The correction
contract forbids adding a migration absent from the failed frozen package.
Therefore the fixture's combined V6→V7 claim cannot be made for this historical
B run. A later separately authorized new V7 release could follow a supported,
verified V6 recovery, but must not be slipped into this correction. Do not rewrite
V1–V3, V5 or repeatables or touch FWM_LAB.

## Prepared candidate and conditional primary-path acceptance

A local, **unapproved and unsubmitted** candidate changes only the duplicate
column in `V6__case2_duplicate_column.sql`:

```sql
CREATE TABLE fwm_cicd_v6_invalid (
    id NUMBER
);
```

SQL SHA-256: `02b7a87fc9c76cb6b83be27f3e00337ab5a102c07f677d7ff8c32444872a0fbd`.
Deterministic ten-file ZIP SHA-256:
`34002a21a1848f4f218543403a9f080c34a78e616baffd8caae161bc7b2626b1`.
The original source reproduces ZIP
`b07f1141889688e669ed6afc60dfce878ff75cee9350c435eda5c6c5896c6b45`;
all other effective file bytes are unchanged. Proposed effects: one empty B-owned
table with one NUMBER column and one successful V6 history entry; no data writes,
new privileges or cross-schema effects. Candidate preparation is not DBA approval.
A future developer commit must reproduce these exact ZIP bytes using the existing
packager; Git SHA/manifest may change while SQL-only deterministic ZIP stays equal.
No demo branch was changed or pushed for this review.

Only after the preceding **historical-state blocker is resolved through a
separately authorized supported path**, and after exact-candidate SQL execution
is explicitly approved, may this sequence proceed. For a future primary-path
failure it additionally requires verified successful Repair, a held stream and
current physical evidence captured by this release:

1. Independently inspect complete native errors and partial DDL/data. Require the
   exact identity/destination above, nine unchanged successes, no failed history
   after verified Repair, no existing V6 table, no external schema writers, and
   no queued/claimed work. Do not auto-handle partial DDL or use manual Flyway CLI.
2. Human DBA uploads the deterministic candidate using the ordinary v2 artifact
   route (current target revision, exact ZIP SHA, unique stable upload key).
   Verify the returned original input_sha256 and every normalized path/hash;
   do not substitute the normalized bundle digest for the original ZIP checksum.
3. With normal authenticated human session/CSRF headers, send:

   ```http
   POST /api/v2/projects/vxfQISv7e-d1Wjhyt1HBE9/deployment-targets/9E4T5dm5YZPhNMEBNk1Zds/correction-reviews
   Content-Type: application/json

   {"failed_run_id":"<eligible-failed-run>","artifact_id":"<exact-candidate-id>","deployment_target_revision":1}
   ```

   Require HTTP 200, review_only=true, ready=true, live_status=VERIFIED,
   blockers=[] and exact current Oracle identity/history/catalogue. For the
   existing historical Run ID this request must remain rejected; never replace
   `<eligible-failed-run>` without reviewing its actual evidence.
4. Human Admin/Operator with current Project/target execute and database/schema
   operate grants acknowledges partial-DDL review, then sends:

   ```http
   POST /api/v2/projects/vxfQISv7e-d1Wjhyt1HBE9/deployment-targets/9E4T5dm5YZPhNMEBNk1Zds/correction-approvals
   Content-Type: application/json

   {"preflight_id":"<fresh-review-id>","input_sha256":"34002a21a1848f4f218543403a9f080c34a78e616baffd8caae161bc7b2626b1","partial_ddl_reviewed":true,"note":"<actual DBA inspection and evidence reference>"}
   ```

   Require HTTP 201, the approved hash, short expiry and stream_held=true. A
   service account cannot approve. Review may be prepared before the Git push,
   but approve only when the exact build/package is ready to submit within the
   ten-minute window. Expiry means STOP/fresh review, never bypass.
5. Developer commits only corrected V6 to the trusted demo branch and pushes
   after source-change authorization. Use existing packager/VALIDATE to prove
   the exact ZIP. Pin that checksum and current target in protected execution
   approval; select a dedicated short-lived token with read/upload/preflight/
   execute only on this target, matching principal membership/database grants.
   Never put Oracle passwords in Jenkins or substitute an Admin token.
6. Run the existing job with ENVIRONMENT=develop, MODE=EXECUTE and both explicit
   authorization flags true. Keep its normal authenticated Jenkins input gate.
   Its upload may have a new Artifact ID but must have the same approved ZIP.
   Require a fresh ordinary executable preflight VERIFIED/ready/no blockers,
   the exact Pending set, HTTP 202 + Location/Retry-After and one Run ID. Jenkins
   has no pause before its first preflight; stage timing must fit the existing
   approval TTL. If it expires, stop and repeat only read-only review/preflight
   preparation under existing controls; do not retry a submitted SQL attempt.
7. Poll the original Run ID to completed=true within the bounded timeout. Collect
   native stdout/stderr/JSON, SQL path/version, migrate count, separate Repair
   outcome, logs, run status, immutable execution record/ETag and audit. Require
   corrected migration SUCCEEDED, Repair SKIPPED and Jenkins SUCCESS. For the
   ten-file B candidate require exactly one new V6 success; combined V6/V7 is
   only valid when V7 was already pending in the eligible original frozen set.
8. Independently compare all earlier physical history fields (rank/version/
   description/type/script/CRC/installed_by/full timestamp/execution_time/success),
   application objects/data and expected new table metadata. Require each new
   version successful exactly once and all previous history unchanged. Verify
   the old failed Run ID/record bytes/checksum and Jenkins #11 FAILURE unchanged.
   Verify consumption points to the one corrected Run ID and hold clears only
   after successful physical verification. Do not run a second migration for a
   no-op proof. Only reconcile the original exact key/request if a lost response
   makes replay necessary and demonstrably safe.
9. Revoke the temporary service token through existing administration after
   evidence capture; verify it returns 401. Preserve SQL, Git/build URLs, artifact
   IDs/checksums, review/approval IDs, request/key, run/log/record/audit and Oracle
   snapshots. No cleanup, manual Repair, Clean, Baseline or history deletion.

Any failed/unsafe/unknown Repair, identity/hash/history drift, unexpected Pending
set, expired/revoked approval or uncertain POST outcome stops further submission.
Preserve NEEDS_REVIEW/hold and reconcile the existing attempt; never create a
second key to force execution.

## Finish acceptance: historical B and new corrected-version C

Preparation updated 2026-10-19. PR #13's production code and SYS-qualified
helper hash are unchanged by this planning update. Full CI previously passed
at bdab6af8; require the updated head's checks to pass before merge. Targeted helper
reproduction/SYS-resolution tests, WAL backup CLI tests and populated v9→v10
original-key restore tests were rerun successfully. Helm lint/template passed
without a GitOps edit. The new demo branch's 26 packaging/client tests passed.
No live SQL was executed during this preparation; the Oracle observations above
belong to the preceding read-only review. Current C-user absence, the future
published fixed digest and the live DB/key restore remain execution gates, not
facts claimed by this proposal.

### Smallest historical reconciliation design — not implemented or authorized

PR #13 cannot reconcile B as-is. Implementing one narrowly scoped **Manager-owned
reconciliation attempt linked to the exact original failed Run ID** is the
smallest supported path; this is separate from the new-failure acceptance below.
It must:

1. Require explicit DBA authority for this settled run, the original frozen ZIP,
   current target/account/revision/runtime, DBID/PDB/schema, current successful
   physical history, exactly one matching failed V6 row/CRC and native info/
   validate isolating only that failure. Use the original frozen SQL for Repair,
   never corrected SQL that could realign successful history. A current baseline
   cannot manufacture missing historical physical evidence; record that limit
   and the DBA's inspection explicitly.
2. Under the existing stream lock, recheck current evidence/authority and run
   native Repair **once through Manager**, with no migrate. Persist intent and
   separate native command/result/before-after evidence and audit linked to the
   old run. Verify only failed V6 was removed; every successful physical row and
   object-catalogue fingerprint is unchanged; info/validate succeeds. An
   interrupted, failed, unsafe or unknown attempt remains held without retry.
3. Keep original run Dn45dSyyZbrXzR-JhnScYi, its UNSAFE assessment, frozen record,
   SQL bytes and Jenkins #11 FAILURE unchanged. Append a new immutable
   reconciliation result with its own ID/time; do not update the old
   repair_operations row or insert pretend evidence into the old record.
4. Keep the stream held after verified reconciliation. Let the existing exact-
   artifact review/approval use the new linked verified evidence for this
   historical failure, then execute only approved corrected V6. Its original
   package had no V7; any later V7 is a separate reviewed release.

This requires a small explicit implementation and fault/restart/concurrency
checks; no such endpoint or append-only reconciliation result exists in PR #13.
It is not a generic hold-clear, history editor, Repair retry engine or user-facing
Flyway CLI. Do not authorize or perform an ad hoc CLI/SQLite workaround in its
place. **Historical B remains untouched in the proposed C acceptance scope.**

### Exact fresh-C real acceptance package

Use existing Oracle FREE/FREEPDB1, DBID 1514744704, proposed new local account/
schema **FWM_WZ260_C**. Create it only if absent; otherwise STOP rather than reuse
or clear it. Proposed setup is USERS quota 8 MiB, a privately generated password,
the same six schema privileges already reviewed for B, and only this extra
metadata privilege: `GRANT SELECT ON SYS.V_$DATABASE TO FWM_WZ260_C CONTAINER=CURRENT`.
No roles, DBA/ANY DICTIONARY privileges, other-schema grants or changes to B.
After authorization, verify all SYS-qualified probe queries **as C**, not just
as SYS. Own history plus existing PUBLIC access to SYS.ALL_OBJECTS/ALL_TABLES/
DUAL needs no extra dictionary grant.

Use existing Manager Project `vxfQISv7e-d1Wjhyt1HBE9`, database target
`oracle-free-lab`, new Deployment Target `oracle-dev-recovery-c` and mapped
account `Jenkins recovery C execution account`, DEV. Record generated IDs and
pin the exact C destination before any submit. Use a dedicated service account
and token scoped only to C, at most two hours, revoked after evidence capture.
Never give its token B authority. Human DBA approval and normal Jenkins input
remain required.

The [prepared manifest](evidence/oracle-recovery-c-proposal-20261019.json) records
all SQL hashes, source paths, draft SQL, local preparation commits and setup
scope. The deterministic packages are:

| Phase | File count | ZIP SHA-256 | Required real result |
| --- | ---: | --- | --- |
| Seed V1–V5 + five repeatables | 10 | `3bced62f03203099b2fd853c0860f118e87165c77baaaedcc1e9e58097cd881b` | Jenkins SUCCESS; exactly ten native migrations and ten Success rows. |
| Invalid V6 + pending V7 | 12 | `2f023b6879fc1a4dc5026f7f9e56c3fc769629a702bafe4cd7bbcb8a70c3e65c` | One V6 attempt, ORA-00957, Jenkins FAILURE, no V7 execution; automatic native Repair executes once and SUCCEEDED; only failed V6 history removed, ten earlier rows unchanged, stream held. |
| Correct only V6 | 12 | `c8817b90f1d5dba84b98ff916b3db41a090d385ed9fdb714aa02745ae2bf3cd1` | After fresh exact-ZIP DBA approval: only V6 then V7 execute; twelve Success rows; earlier ten physical rows unchanged; hold released only after verification. |

The existing V1–V3, V5 and five effective repeatables are byte-identical to the
reviewed demo sources. New V4 creates only `FWM_RECOVERY_V4_MARKER(ID NUMBER)`;
V7 creates only `FWM_RECOVERY_V7_MARKER(ID NUMBER)`. Invalid/corrected V6 uses the
same reviewed table and filename as B, scoped by C's account; the correction
removes the duplicate `id NUMBER` column. V2 seeds the same three demo rows as
the original package. There are no cross-schema references or destructive SQL.
Seed creates the existing customer/configuration objects, sequence, PL/SQL,
views, trigger and V5 note table, plus V4. No successful file changes between
phases; V7 is frozen in the failing package before correction is approved.

Local branch `demo/oracle-recovery-c-20261019` contains three prepared SQL commits;
none was pushed. After the approved fixed release exists, the only required
non-SQL demo changes are reviewed release/schema-10 pins and protected C mapping/
exact-package approvals. Rebase/recreate the SQL commits on those approved pins,
retain ZIP hashes above, and record actual pushed Git SHAs. Push seed first and
wait for its result, then failure, then the correction after DBA review. Do not
push all phases ahead and accidentally build the final HEAD as the seed.

Use the existing Jenkins multibranch job/Jenkinsfile, no new platform or pipeline
framework. Verify/scoped identity, upload, preflight, normal authenticated approval,
HTTP 202, stable phase-specific key, poll, logs and frozen record follow existing
controls. Permit one migrate attempt per phase; no new key/retry after uncertain
submission. Require VERIFIED/ready/no blockers and exact history/Pending set at
each gate. The invalid phase must show real native Repair SUCCEEDED, not merely
an eligibility assessment or subprocess fixture. Any unexpected state stops the
remaining phases and preserves NEEDS_REVIEW/hold and all evidence.

For every phase retain Git SHA, real build URL, original ZIP hash, destination/
Artifact/Preflight/Run IDs, native migrate/Repair outputs, independent Oracle
history/objects/data, logs, immutable record/ETag and audit. The failed C run's
frozen record must remain byte-identical after correction; its original Jenkins
build stays FAILURE. B and FWM_LAB history/records/grants stay unchanged. No
cleanup, manual Repair, Clean, Baseline, hold removal or extra migrations.

The single proposed operational authorization covers: merge reviewed PR #13;
standard CI publication; a quiet-window DB/key backup and isolated network-none
restore with the actual published digest; digest-only GitOps rollout after those
checks pass; narrow C provisioning/grants/application mapping and short-lived
token; approved demo-only source/pin pushes; and the exact three C SQL phases
through normal Manager/Jenkins approval controls. The image digest is deliberately
not invented before publication. Source/CI/digest/restore/identity/metadata gates
remain mandatory inside that authorization; any mismatch stops before SQL.
It excludes all historical B reconciliation execution and implementation.

### Resume existing C after the correction-review timeout fix

CASE 1 run `WM1GbaXtuEPmTPiSWwTSEf` succeeded. CASE 2 run
`59pFGk-geSBnFBp6Wnkb9N` remains FAILED with automatic Repair SUCCEEDED;
its stream stays held with ten unchanged Success rows and V6/V7 Pending.
The uploaded correction Artifact `ItCwCFglMFnGuavEv0rjaC` has approved ZIP
SHA-256 `c8817b90f1d5dba84b98ff916b3db41a090d385ed9fdb714aa02745ae2bf3cd1`.
Its first review returned HTTP 500; no review/approval was persisted. The timeout
fix does not authorize another live request, merge, publication, rollout or SQL.

After review and separate release/deployment authorization, publish through CI,
verify source/provenance/digest and preserve the pinned Flyway distribution.
Back up the current SQLite database and original key separately; verify an
isolated schema-10 restore with the new image before digest-only GitOps rollout.
Verify the same installation and Oracle FREE/FREEPDB1 DBID 1514744704,
FWM_WZ260_C, Project `vxfQISv7e-d1Wjhyt1HBE9`, Deployment Target
`wwKRr7q01NuRmAynI9zQq2`, revision 1, existing account credential version 2,
unchanged grants/quota, held stream and original frozen records. Reuse the exact
candidate for a fresh review with a client response timeout of at least 60s.
Require VERIFIED/ready/no blockers and only V6/V7 Pending, then obtain independent
human DBA partial-DDL review and exact-checksum approval through the existing
controls. Only afterward push the reviewed corrected V6 and execute V6/V7 once
through Jenkins. Revoke temporary tokens after evidence collection. Do not rerun
CASE 1/2, re-execute invalid V6, recreate C or change B/FWM_LAB or their history.
