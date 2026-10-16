# Oracle Free lab acceptance — 2026-10-16

**Current result: Oracle Free / real Flyway Manager preflight and V2 execution PASS. Completed-job restart, idempotency, immutable continuity and non-destructive negative checks PASS. Oracle 19c and 21c remain NOT VERIFIED.**

The owner authorized the isolated disposable lab, then a focused fix for the reproduced configuration defect and real application acceptance using the existing database. The first round provisioned the lab and applied V1 directly with Flyway. This follow-up changed only Flyway configuration serialization, its regressions and acceptance documentation; it restored the same temporary loopback manager and tunnel and applied V2 through the normal manager APIs. No database was reprovisioned, existing Kubernetes workloads or unrelated repositories changed, or Flyway Manager deployed. The temporary manager and tunnel are stopped after evidence capture.

The [initial provisioning/direct-CLI evidence](evidence/oracle-free-lab-20261016.json) is preserved, including the original failed manager preflight. The [real manager V2 acceptance evidence](evidence/oracle-manager-v2-20261016.json) contains the follow-up preflight, before/after SQL, durable run/execution/log/audit data, frozen-file checksums, negative checks, restart proof and final check results. Private credentials, SQLite data, keys, source archives and the installed runtime remain outside Git under `/tmp/flyway-oracle-acceptance-20261016`; private follow-up working data is under `/tmp/flyway-manager-integration-20261016` (directories mode 0700; credential/config files mode 0600).

## Provisioned resources and discovered endpoint

| Item | Verified value |
| --- | --- |
| Context / authenticated principal | `k8s-admin-public` / `kubernetes-admin`, group `kubeadm:cluster-admins`; administrator permissions confirmed before changes |
| Dedicated namespace | `flyway-manager-oracle-lab` |
| Workload | StatefulSet `oracle-free`, one pod `oracle-free-0`, on `k8s-worker1` |
| Readiness | Startup/database health passed; Pod Ready, zero restarts at final inspection |
| PVC | New `oracle-data`, Bound, 4 GiB, RWO, `longhorn-workers` |
| Backing volume | `pvc-992cf679-0bfe-4f4f-880c-539764893545`; distinct new claim/volume identities captured in evidence |
| Storage behavior | Two Longhorn replicas; Retain reclaim policy. Worker/storage capacity checked before creation |
| Resources | Requests: 250m CPU / 2 GiB memory. Limits: 2 CPU / 3 GiB memory; namespace quota bounds one pod, one PVC, 4 GiB and one Service |
| Service | `oracle-free`, ClusterIP `10.105.181.191`, TCP 1521; no external IP, NodePort, LoadBalancer or ingress |
| Actual Service DNS | `oracle-free.flyway-manager-oracle-lab.svc.cluster.local`, resolved inside the lab pod to the actual ClusterIP |
| Ready EndpointSlice destination | `10.244.0.131:1521`, with targetRef pointing to the new pod UID |
| Actual listener/PDB | Listener advertises `freepdb1`; SQL confirms PDB `FREEPDB1`, READ WRITE |
| Actual database | **Oracle AI Database 26ai Free**, release/version `23.26.3.0.0` |
| Execution account/schema | `FWM_LAB` |
| History table | `FWM_LAB."flyway_schema_history"` |

The Service DNS/domain was read from resources, the pod's DNS configuration and a real lookup. PDB/service identity was checked using listener output, `v$pdbs`, `v$services` and an authenticated execution-account connection. The image retains a template database creation date/DBID; DBID alone was not used as isolation proof. Fresh namespace/PVC/volume/pod identities establish the separate lab storage boundary.

## Image, terms and runtime provenance

Oracle image: `docker.io/gvenzl/oracle-free:23.26.3-slim@sha256:6d61d267a3b978c24c5ac1790e62e927416a0aec446bd86e4b3a1527562757bd`. A TLS-verified registry read of the public tag's raw manifest produced that digest; the running pod reports the same image ID. Registry labels identify Gerald Venzl and [the maintainer repository](https://github.com/gvenzl/oci-oracle-free), whose current support table includes this image family. This checks registry identity/source attribution, not a signed reproducible-build attestation.

The database's bundled `LICENSE` identifies the [Oracle Free Use Terms and Conditions](https://www.oracle.com/downloads/licenses/oracle-free-license.html). Those terms allow internal application development/testing subject to their conditions; no registration/license restriction was bypassed and no database binaries were republished. The build scripts' Apache license was not treated as the database license.

Flyway **13.10.3** was acquired from the versioned HTTPS archive linked by [Redgate's official CLI documentation](https://documentation.red-gate.com/flyway/reference/usage/command-line). The bundled EULA and [product-specific terms, section 25](https://www.red-gate.com/support/license/product-specific-terms/) were inspected; Community is offered as Freeware. No paid license, trial activation or cloud service was used. The package's third-party and JRE notices were retained.

| Runtime evidence | Value |
| --- | --- |
| Published archive SHA-1 | `6bd32305c1d4489a864590f6952124ec6cb4a866`, matched |
| Recorded archive SHA-256 | `f0451f0ef9d1c558d5e1fd5a35ad65f039bebecd4eb2007a87022da21fc2dccf` |
| Complete-file manifest | 1,458 files, no archive links/special members or tree symlinks |
| Manifest SHA-256 pin | `a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c` |
| Java | Bundled Eclipse Temurin `25.0.4+7-LTS`; 512 MiB heap cap during checks |
| Oracle JDBC | Bundled `ojdbc11-21.18.0.0.jar` |
| Oracle modules | Bundled `flyway-database-oracle-13.10.3.jar` and proprietary support module |
| Existing installer/readiness | `runtime-install` succeeded; `/api/v1/runtime` returned `ready=true`, `integrity=verified`, `bundled_java_and_oracle_jdbc_available` |

The manifest was generated only after establishing the archive's official HTTPS origin and matching its published checksum. It is a local integrity pin, not an upstream signed manifest. Upstream provided SHA-1 at the documented checksum link; a SHA-256 sidecar returned 404. Exact installed-tree hashes, file coverage and absence of links were rechecked before every direct CLI operation. No installed vendor file was modified.

## Initial isolation and direct V1 execution evidence

Before any migration, the new namespace, PVC, Pod, Service and readiness were independently checked through Kubernetes. Cilium's realized policy showed ingress and egress enforcement: ingress is restricted to namespace-local TCP 1521; egress permits DNS to CoreDNS. The database pod has no mounted service-account token, runs as non-root, drops capabilities and disallows privilege escalation. Administrative loopback port-forward access remains a Kubernetes administrator capability.

The execution account was created only in this lab PDB, with new random credentials, exactly `CREATE SESSION` and `CREATE TABLE`, a 32 MiB USERS quota, no roles and no database links. An independent SYS observer confirmed those grants and zero grants from the locked `FWM_GUARD` canary schema to either `FWM_LAB` or PUBLIC. A read of its canary table as `FWM_LAB` failed with the expected `ORA-00942` both before and after migration. All setup/observer SQL ran in this new database; existing databases were not connected to or modified.

| Check | Before | After |
| --- | --- | --- |
| Actual Flyway `info` | Exit 0; empty schema; V1 Pending | Exit 0; schema version 1; V1 Success; installedBy FWM_LAB |
| Actual Flyway `validate` | Exit 0; validationSuccessful=true; no invalid migrations | Same successful result |
| Controlled `migrate` | Not attempted until isolation and validation passed | Exit 0; success=true; migrationsExecuted=1; no warnings |
| Execution schema tables | Zero | `FWM_ACCEPTANCE_PROBE` and `flyway_schema_history` |
| Probe data | Absent | Exactly `1,isolated-oracle-free-acceptance` |
| SQL history observation | No table | One row: rank 1, version 1, script `V1__isolated_lab_probe.sql`, success 1 |
| Canary | `1,unchanged-lab-canary`, access denied to execution user | Same value; access still denied |

The single migration first asserts SESSION_USER/CURRENT_SCHEMA `FWM_LAB` and CON_NAME `FREEPDB1`, then creates one probe table and inserts one row. SQL SHA-256: `4720b37c48960caa7e46385edbd8809421f59fa09918cb12727b51b7f6965602`. It ran once via the verified direct CLI, never via a manager job. Clean, repair, reset, intentional migration failures, partial-DDL tests and automatic retries were not run. `info` in this CLI reports an empty `database` field; `validate`, listener output and independent SQL supply the PDB identity evidence instead.

## Initial manager API registration and reproduced preflight failure

The existing APIs successfully bootstrapped the isolated local manager, registered target `oracle-free-disposable-lab`, schema `FWM_LAB`, a mapped execution account with a write-only credential, and an explicit schema-scoped operational grant. The target used DEV/TCP and the temporary route `127.0.0.1:11521` through `kubectl port-forward service/oracle-free`; this is distinct from the real in-cluster Service DNS above. A real immutable migration artifact was uploaded through `/api/v1/artifacts`.

In the initial round, `POST /api/v1/migration-preflight` returned HTTP 200 with `ready=false`, blocker `ORACLE_NOT_VERIFIED`, and Flyway's error **“No Flyway database plugin found to handle ********”**. No manager job had been submitted in that initial round. This was the failure subsequently fixed and reverified below; repair remains outside this acceptance round.

The read-only A/B reproducer used the same installed CLI, credentials, SQL and destination:

```properties
# Successful direct CLI configuration
flyway.url=jdbc:oracle:thin:@//127.0.0.1:11521/FREEPDB1

# Changing only the URL to the manager's generated escaping reproduces exit 1
flyway.url=jdbc\:oracle\:thin\:@//127.0.0.1\:11521/FREEPDB1
```

At the initial revision, `escapeProperty` in `internal/flyway/runtime.go` added these backslashes. This is a demonstrated manager configuration-serialization defect with Flyway 13.10.3, not evidence of a missing Oracle plugin: both Oracle module JARs are present, and unescaped direct CLI info/validate/migrate succeed. It was recorded without changing product implementation or relaxing its fail-closed checks. An initial acceptance-client login also returned 401 because it omitted the required `provider: local`; correcting that client request succeeded without product changes.

## Configuration fix and actual-loader regression

The same pinned Flyway 13.10.3 reproduced the failed manager preflight before edits. Its legacy loader trims lines, doubles backslashes, expands environment references and then invokes Java property parsing. A generic Java-properties escaper therefore changes the JDBC URL and cannot preserve arbitrary credentials. The fix is typed private `flyway.toml` with `[environments.runtime]`, `[flyway]` and `[flyway.oracle]`, preserving the semantic policy map, complete runtime pin, fixed options, restricted subprocess environment and private file/workspace modes. This is a configuration-format correction, not an Oracle URL replacement or SQL rewrite.

The environment's URL/user/password use quoted basic strings plus Flyway's documented `!{...}` whole-value literal escape. That preserves punctuation, backslashes, supported whitespace, Unicode and resolver-looking text without evaluating it. DEL/C1 characters receive explicit Unicode escapes. Secret masking includes the exact TOML wire representation and retains raw, URL, JSON and legacy property-escaped masking. The frozen nonsecret configuration snapshot does not change.

A pinned upstream resolver limitation is explicit: Flyway 13.10.3 fails to recognize `!{...}` around CR, LF, NEL, LS or PS. Credentials containing those line terminators, NUL or invalid UTF-8 fail closed before subprocess launch. They are not silently changed or moved to argv/environment. Existing unquoted Oracle username validation remains intact. This boundary and ordinary/special values are covered by regressions.

The optional offline regression invokes the actual pinned `ModernConfigurationManager`, compares synthetic credential digests and connection/policy fields, and checks disabled SQL*Plus through the Oracle extension. It tests legacy raw/escaped URLs, TOML values, supported controls, literal resolver/wrapper syntax and the upstream line-terminator boundary. It connects to no database. See [runtime regression instructions](../internal/flyway/README.md#configuration-serialization-and-real-loader-regression).

## Real manager V2 workflow

The fixed final-build candidate returned HTTP 200 with `ready=true`, `live_status=VERIFIED`, successful info/history inspection and validation. Independent SQL confirmed SESSION_USER/CURRENT_SCHEMA `FWM_LAB`, PDB `FREEPDB1` and service `freepdb1`. SQL/history/probe/canary snapshots before and after preflight were identical. The API still correctly leaves broad DDL privileges/DBID identity unverified; the independent lab SQL supplies the specific identity and isolation evidence.

The upload contains the original **unchanged V1 bytes** and a harmless V2 with an explicit SESSION_USER/CURRENT_SCHEMA/PDB assertion and one INSERT into the existing probe table. Including V1 supplies Flyway's complete resolver view; it does not rerun V1. No history table was edited manually.

| Evidence | Verified result |
| --- | --- |
| Submit | HTTP 202; durable Run ID `q0GHoTkZokkhjB2KF0_SJF` |
| Observed states | `QUEUED → PREFLIGHT → RUNNING → SUCCEEDED` |
| Authenticated actor | `oracle-lab-admin`, user ID `T-07_V3SBssUXI4Ieecvej`; run and all eight run audit events match |
| Pinned subprocesses | `info → validate → migrate → info → validate`; every exit 0/success, no ambiguous result |
| Actual migrate | One migration executed: V2; migration outcome SUCCEEDED, repair outcome SKIPPED |
| Centralized logs | Ten subprocess lines match recorded stdout/stderr exactly, plus one SUCCEEDED orchestration event |
| Artifact | `yH2EEwBpkqfF42WhmkLn1R`, bundle SHA-256 `193ab7ed30de66e637a7d17fa1d6bc501345218989671dc7856782aa4bc0d9cc` |
| V1 bytes | SHA-256 `4720b37c48960caa7e46385edbd8809421f59fa09918cb12727b51b7f6965602`, unchanged |
| V2 bytes | SHA-256 `77dfe761086f9c1a6ec6d84013884254fa017d14bfad22832f78c593f5e8c935` |
| Frozen snapshot | Original artifact/file references and content digests match the immutable run items; frozen target/schema/account/policy/runtime pin and actor persisted |
| V1 SQL history | Rank 1, checksum 1105310609, success 1; original installed timestamp unchanged |
| V2 SQL history | Rank 2, `V2__manager_lab_probe.sql`, checksum 739407795, success 1; installed by FWM_LAB |
| Probe data | Original row 1 unchanged; exactly one new row `2,manager-v2-acceptance` |
| Canary/privileges | `1,unchanged-lab-canary`; execution user's two system grants, no roles and no canary grants remain unchanged |

### Negative checks, continuity and restart

- Unavailable loopback connection: HTTP 200 diagnostic response, `ready=false`, `ORACLE_NETWORK_FAILED`; no job or SQL mutation.
- Incorrect execution credential: HTTP 200 diagnostic response, `ready=false`, `ORACLE_AUTHENTICATION_FAILED`; the original private credential was restored through the write-only API. No Oracle password was changed.
- Operator without a scoped grant: HTTP 403 `FORBIDDEN` before Oracle execution.
- Equivalent submissions with `Idempotency-Key: oracle-manager-live-v2-20261016`: HTTP 202 with the same Run ID both during the run and after restart. A different canonical request using that key returned HTTP 409 `CONFLICT`.
- A subsequent upload containing only the unchanged V2 reconstructed the original V1 from the manager's immutable stream bytes. Both resolver entries were Success with zero Pending; preflight validation passed and SQL snapshots stayed identical.
- A graceful restart using the same private manager data/config preserved the completed SUCCEEDED run. There remained one job with `claimed=0`, exactly five recorded subprocesses and **one total migrate**. Oracle history and row counts did not change after restart or replay of the idempotent request.
- No partial-DDL failure, repair, clean, reset, manual history edit or automatic SQL retry was intentionally invoked. Non-destructive diagnostics were completed before migration submission.

One read-only bad-credential probe was interrupted by a local manager restart during test setup. Its cleanup request also lost the connection. The private manager credential was restored before continuing; the probe was rerun successfully on a stable process. No job had yet been submitted, and independently observed Oracle state was unchanged. This interruption is not counted as a passed diagnostic. An earlier acceptance-client artifact upload also received the correct 403 until the administrator received an explicit diagnostic-target grant; global Admin metadata access does not imply operational grants.

## Required verification

The final source passed `make check`, `make test-race`, `make test-e2e` and `make security`, with `FLYWAY_TEST_RUNTIME_CONFIG` pointing to the already approved pin so the actual-loader regression ran in both Go suites. The browser suite passed all 10 tests; the frontend unit suite passed all 11. Security reported zero npm vulnerabilities and zero reachable/imported-package Go vulnerabilities, with one module-only advisory. Redocly recommended OpenAPI validation passed with the same two previously documented style exclusions. No new dependency, API route, runtime download, recovery feature or deployment was introduced.

`git diff --check` and Gitleaks 8.30.1 on the staged publication files pass. Inspection of every staged filename and file signature finds no credentials, SQLite database, master key, bootstrap token, private configuration or runtime binary; the known private lab credentials are absent from all staged content.

## Retained state and acceptance gaps

The existing dedicated database namespace/workload/PVC/Service remain ready for review; they were reused throughout this follow-up, with no provisioning or workload changes. Random database credentials remain in Kubernetes Secret `oracle-lab-credentials`; local credentials and the encrypted manager registry are private under `/tmp/flyway-oracle-acceptance-20261016`. The temporary local manager and loopback tunnel were terminated after evidence capture, so the recorded local registry route is currently offline. To inspect that same local registry, restart the binary with `FLYWAY_MANAGER_CONFIG` pointing to that directory's `manager-config.json` and reopen the same loopback Service tunnel; this is not a cluster deployment. Storage uses Retain; no deletion or cleanup of persistent resources was performed.

Oracle 19c/21c, TCPS, dedicated history-schema/CURRENT_SCHEMA behavior, broader PL/SQL/package/repeatable cases, partial DDL, failure/repair, crash/reconciliation, external-writer exclusion and production operational acceptance remain unverified. The minimal PL/SQL destination guard passed on this Free version only. The demonstrated manager configuration defect is fixed and the real application workflow is verified on this Oracle Free version. Broader production readiness and independent Oracle 19c/21c acceptance are not implied. The focused fix, regressions and sanitized evidence are prepared for the authorized repository publication; the final response reports the verified Git commit and push result.
