# Phase 6 decision: a small multi-database execution console

Assessment date: **2026-10-17**. Baseline inspected: `c93f15884fa06a326549badfdc828cb91e31d003`, registry v7, approved Flyway Community 13.10.3. **Recommendation: PostgreSQL first**, using one registered database per Instance and one schema per migration stream. Preserve the Oracle implementation and its safety policy. Add SQL Server next only after catalog and recovery acceptance; treat MySQL and MariaDB as separate engine qualifications.

This is a research and architecture decision package. No application code, registry migration, database connection, deployment, design change, runtime upgrade, push or merge was performed. CLI evidence was collected offline from a fully manifest-verified existing distribution. The other files provide the [CLI inventory](flyway-cli-capability-catalog.md), [database catalog](flyway-database-support-matrix.md), [edition boundaries](flyway-edition-feature-matrix.md), [source-backed gaps](current-product-gap-analysis.md), [architecture](multi-database-reference-architecture.md) and [acceptance plan](prioritized-implementation-plan.md).

## Product boundary

Flyway Manager centrally registers connections and execution accounts, accepts immutable SQL artifacts, executes approved Flyway operations, and retains auditable history and diagnostics. Installation remains one Go service with embedded React, SQLite and the existing encrypted credential store. Docker and Kubernetes remain optional operating choices, not installation prerequisites.

Projects remain organizational references to Instances or schemas. They neither own connections nor introduce execution privileges. Application permissions select authorized destinations and accounts; database grants determine what SQL can actually do. A schema allowlist is not a SQL sandbox.

Do not add a SQL editor, SQL approval service, database provisioning, general database administration, data browsing, schema synchronization, state-based deployment, Git hosting, enterprise secrets marketplace or cross-database orchestration. A pending Flyway row means pending relative to the captured resolver files; it does not prove external source completeness.

## Scope and priorities

| Priority | Product commitment | Exit boundary |
| --- | --- | --- |
| **P0** | Preserve Oracle connection/discovery, migration jobs and constrained repair; credentials, RBAC/PROD grants, projects, immutable artifacts, historical Info/logs, v7 data and runtime pin | No silent configuration, identity, snapshot or recovery-policy change |
| **P1** | Engine-aware profile and identifiers; PostgreSQL connection test and schema discovery; explicit account mapping; existing native Info, preflight, validate/migrate pipeline and history for a qualified PostgreSQL database/schema | Password/SCRAM, verified TLS, pre-existing database and schema, same-schema history, ordinary transactional SQL; no automatic PostgreSQL repair |
| **P2** | Standalone recorded Validate; bounded non-secret placeholders; measured catalog aggregation; later qualification of PostgreSQL nontransactional statements, then SQL Server and MySQL/MariaDB | Each needs its own policy and acceptance; these are not launch dependencies |
| **P3** | General Repair, Baseline onboarding, Clean, Undo, history marking/skipping, callbacks, executable artifacts, arbitrary config/JDBC URLs, commercial comparison/report/provisioning tools, MCP | Excluded from next slice; some remain permanently outside this console |

For P1, keep standalone Validate out of the UI: validation already runs in preflight and before/after migrations. Recorded Info supplies the first independent inspection workflow. Repeatable `R__` SQL already passes the artifact pipeline; verify its PostgreSQL behavior without adding a new authoring feature. Keep placeholders disabled until the separate P2 contract freezes values and redaction rules into execution evidence.

## Why PostgreSQL first

| Candidate | Compatibility and dependencies | Scope and testing work | Product value and recommendation |
| --- | --- | --- | --- |
| **PostgreSQL** | Pinned `flyway-database-postgresql` and pgJDBC **42.7.14** already present; password/SCRAM can reuse encrypted password storage | Database then schema; new lowercase identifier/discovery logic; transactional failures differ from Oracle; concurrent indexes require a deferred lock/transaction policy | Strong next example of a different resource model, modest dependency burden. **First** |
| **SQL Server** | Pinned `flyway-sqlserver` and Microsoft JDBC **12.10.2.jre11** present | Catalog/schema separation; login versus user; collation-sensitive identity; `GO` batches, permission visibility, transactional exceptions and TLS trust. Integrated/Entra authentication adds dependencies | Valuable when there is a concrete Microsoft workload. **Second**, SQL password only initially |
| **MySQL/MariaDB** | Pinned `flyway-mysql` and MariaDB Connector/J **2.7.14**; **no MySQL Connector/J**. Redgate documents a MariaDB fallback for MySQL, with URL compatibility caveats | Database equals schema; case rules vary with server/OS; implicit DDL commits and account host identities. Qualify MySQL and MariaDB independently | Useful shared adapter family later. **Third**; defer MySQL if fallback cannot pass without changing the approved runtime |

Driver versions are observed in the [verified runtime components](evidence/runtime-components.txt), not inferred from current download pages. Relative engineering effort is architectural judgment, not a delivery estimate. An actual SQL Server demand can change ordering after the same gates; no demand or workload inventory was supplied.

## Smallest next implementation slice

Implement a PostgreSQL profile alongside Oracle, one logical database per target, one existing unquoted lowercase schema per stream, same-schema history, an explicitly mapped password account, and TLS with server identity verification. Qualify **one declared PostgreSQL major first** (proposed 17); keep other majors unavailable until tested. Connect/discover/save, choose artifact, inspect Info, run the existing preflight and async migration, and inspect historical logs must work end to end.

The first engineering increment should establish backward-compatible profile storage and a PostgreSQL **connection/discovery + native Info** vertical slice. It must keep PostgreSQL Migrate disabled. The next increment enables Migrate only after transaction, failure, hold and restart gates pass. This avoids advertising full engine support when only a test connection works. See the [gated sequence](prioritized-implementation-plan.md).

No new runtime or JDBC file is needed for that proposal. If qualification reveals a missing dependency or an incompatible pinned runtime, stop that engine's promotion and submit a separate runtime decision; do not add jars at runtime.

## Evidence and unresolved limits

Actual `-v` and JSON `list-engines` identify **Community**, with 39 loaded engine labels. Help exposes commercial commands too; visibility is not entitlement. `help mcp` fails with a command-combination error, and SQLFluff reports **not installed**. These observations are retained rather than converted into feature support claims. See [capture method and hashes](evidence/README.md).

The full catalog reconciles the driver-reference index with the support table: several community engines have no verified version, Aurora DSQL lacks its plugin/driver in this pin, Fabric Warehouse is preview on its own page but excluded by the broader support page, and some database version ranges disagree. None is a launch candidate. Newer documentation, previews and roadmap promises do not enlarge 13.10.3's accepted surface.

Existing Oracle evidence is reused from [Projects/Info acceptance](../projects-flyway-info-acceptance.md) and [Oracle Free acceptance](../oracle-free-lab-acceptance.md); it was not rerun. Catalog behavior is also covered by the committed [schema catalog regression suite](../../web/tests/schema-catalog.spec.ts). There is no new PostgreSQL, SQL Server, MySQL or MariaDB live acceptance in this phase. The recommendation is a buildable roadmap, not a declaration that those engines are operational.

Primary references: Redgate's [commands and command tiers](https://documentation.red-gate.com/flyway/reference/commands), [database driver index](https://documentation.red-gate.com/flyway/reference/database-driver-reference), [PostgreSQL driver behavior](https://documentation.red-gate.com/flyway/reference/database-driver-reference/postgresql-database), [SQL Server driver behavior](https://documentation.red-gate.com/flyway/reference/database-driver-reference/sql-server-database), [MySQL fallback policy](https://documentation.red-gate.com/flyway/reference/database-driver-reference/mysql), and [MariaDB driver](https://documentation.red-gate.com/flyway/reference/database-driver-reference/mariadb), accessed 2026-10-17.
