# Disposable PostgreSQL foundation acceptance

Run only against the local lab created by `setup.sh`. The scripts use a pinned PostgreSQL 17 image, bind port 55440 to loopback, require SCRAM and verify-full TLS, and keep credentials/keys/configuration under the private local state directory. They neither contact Oracle nor provision Kubernetes resources.

```sh
scripts/postgres-lab/setup.sh
scripts/postgres-lab/verify.sh
set -a
source "$HOME/.local/state/flyway-manager-postgres-lab/lab.env"
set +a
export FWM_POSTGRES_LAB=1
export FWM_LAB_FLYWAY=/path/to/verified/flyway-13.10.3/flyway
export FWM_LAB_EVIDENCE=/private/path/to/evidence
GOTOOLCHAIN=go1.26.9 go test ./internal/core -run '^TestPostgreSQLRealConnectionAndInfo$' -count=1 -v
make build
(cd web && npx playwright test --config=playwright.postgresql.config.ts)
```

The core integration test owns only a disposable Manager registry. It checks connection/save/rotation, two databases, account and Viewer restrictions, nonexistent history, controlled V1/V2 fixture history, native artifact-relative states, immutable checksums and absence of migration jobs/reservations. It compares normalized complete `pg_dump` output before/after every Info call. Its native `migrate` call is explicitly fixture setup on the local lab; Manager migration remains disabled. Re-running uses the same immutable fixture and a separate absent-history location. Do not change the fixture SQL against existing history.

`TestPostgreSQLProbeLive` in `internal/flyway` additionally accepts `FLYWAY_TEST_RUNTIME_CONFIG`, a mode-0600 JSON file containing the existing `flyway` runtime configuration, together with the private `PG*` environment. It exercises real JDBC identity, PostgreSQL 17, valid and invalid authentication, database, listener, hostname, CA, readonly privileges and native Info. The source tree includes no runtime downloads or driver changes.

After Gates A–D pass, the separately controlled migration stage is executable with:

```sh
scripts/postgres-lab/gate-ef.sh --approved-after-gates-a-d
```

Each invocation creates a fresh, uniquely named disposable database and retains private CLI JSON, SQL fixtures and observations. It tests V1/V2, repeatable/no replay, transaction rollback, partial commit across scripts, semantic checksum failure, cross-schema denial, Missing/Future, concurrent clients and terminated-client rollback. The success check reads native JSON as well as process exit status; Flyway Validate can report failure while exiting zero. No Repair/Baseline/Clean is issued, and history rows are not edited.

This stage does **not** qualify Manager migration or recovery. Manager API idempotency and duplicate submission, concurrent stream reservations, frozen queued credentials/profiles, revocation, predecessor failure, timeout/restart holds, no implicit retry and operator recovery need an engine-specific controlled worker/browser acceptance increment before migration can be enabled. Existing Oracle tests remain regression evidence for Oracle. PostgreSQL has no generic Repair or recovery control; do not edit SQLite or history to release a failed stream. There is no deployment or production acceptance in this package.

The populated-v7 rehearsal is separate. `rehearse-v7-upgrade.py --help` documents explicit backup, key and report inputs. It copies a stopped local backup into a private seed, uses SQLite's backup API, upgrades only the disposable copy and validates all original rows and encrypted credentials. It never starts a migration worker or Oracle adapter. Protect the original external key; v8 has no downgrade migration.

When all evidence has been retained, `teardown.sh` removes only this named lab container, its attached disposable data volume and its private state directory. It is an explicit cleanup action; evidence under that state directory must be copied to a protected destination first.
