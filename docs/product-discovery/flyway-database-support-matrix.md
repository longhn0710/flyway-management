# Database support catalog — documentation versus pinned runtime

Assessment date **2026-10-17**. Covers **all 47 children** of Redgate's [Database Driver Reference](https://documentation.red-gate.com/flyway/reference/database-driver-reference), plus managed-service variants from its [supported databases and versions](https://documentation.red-gate.com/flyway/getting-started-with-flyway/system-requirements/supported-databases-and-versions) page. Upstream support is **not enabled Manager support**. Only Oracle is operationally accepted in this product. No database was contacted in Phase 6.

Versions below distinguish umbrella support-table ranges from driver-page **verified** versions. Neither proves every intermediate/newer server works with this pin. “Latest” is a dated service claim; “N/A” means no verified version, not unlimited support. Certified commercial support and compatible/community support are separate labels; old-server compatibility does not itself require a commercial license under the documented v10+ policy.

Pinned evidence: [39 loaded labels](evidence/list-engines-json.txt), [runtime components](evidence/runtime-components.txt), [complete manifest](../../deploy/runtime/flyway-13.10.3.sha256). A loaded module may lack its JDBC driver. Exact jar versions are in the manifest; the table abbreviates module names. **Y*** means the pinned JDBC implementation reports DDL transaction support, with statement exceptions; **N** means it does not. These flags come from [bytecode declarations](evidence/database-runtime-flags.json), not live rollback tests. Inherited family and native-connector cases are explicit; **?** means not established.

## Priority engines

| Engine | Destination, scopes and history | Authentication and TLS | DDL, commands and qualification |
| --- | --- | --- | --- |
| Oracle | Service/PDB then user schema; current unquoted uppercase identifiers; same-schema history | Pinned ojdbc11 **21.18.0.0** and oracle module. Password path accepted. TCP/TCPS fields exist; TCPS/wallet/TNS are not newly accepted | Implicit DDL commits. Keep accepted Info/Validate/Migrate and narrow failed-row Repair. SQL*Plus is Teams and disabled; Free-lab evidence does not certify new 19c/21c/TLS/dedicated-history cases |
| PostgreSQL | Database contains schemas; role need not equal schema; explicit schema/default history. search_path is not a security boundary | Pinned pgJDBC **42.7.14** and postgresql module. Password/SCRAM first; verified CA/hostname TLS. pgpass/Kerberos/IAM deferred | Ordinary DDL transactional, exceptions remain. Concurrent indexes need separate advisory-lock/transaction acceptance; psql meta-commands unsupported. No new-engine automatic Repair |
| SQL Server | Catalog/database contains schemas; server login maps to database user; case follows collation. Explicit catalog/schema/history | Pinned Microsoft JDBC **12.10.2.jre11** and sqlserver module. SQL password first; Windows/Entra/Kerberos need separate dependencies/identity acceptance. Require encryption and certificate validation | Many transactional DDL operations, with exceptions such as memory-optimized tables. Test GO batches, catalog changes, permissions and failure evidence. No all-schema Clean adoption |
| MySQL | Database=schema; no fictitious extra schema parent. Case varies by server/OS; account identity includes user/host. History in database | **No MySQL Connector/J** in pin. MariaDB **2.7.14** fallback documented, with URL scheme/permitMysqlScheme and auth caveats. TLS must match actual driver | Implicit DDL commits. Test DELIMITER/procedures, auth plugins and partial failure. Option-file auth is Teams; no general Repair or Clean |
| MariaDB | Database=schema; separate engine qualification even when sharing mysql module; history in database | Pinned MariaDB **2.7.14**. Password first; use verified **2.x TLS settings**, not assume 3.x sslMode or 3.4 zero-config TLS | Implicit DDL commits; server/parser/account differences require independent tests. Bundled driver is not all-version product support |

Sources: [Oracle](https://documentation.red-gate.com/flyway/reference/database-driver-reference/oracle-database), [PostgreSQL](https://documentation.red-gate.com/flyway/reference/database-driver-reference/postgresql-database), [SQL Server](https://documentation.red-gate.com/flyway/reference/database-driver-reference/sql-server-database), [MySQL](https://documentation.red-gate.com/flyway/reference/database-driver-reference/mysql), [MariaDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/mariadb), [transaction handling](https://documentation.red-gate.com/flyway/flyway-concepts/migrations/migration-transaction-handling), [pgJDBC TLS](https://jdbc.postgresql.org/documentation/ssl/), [Microsoft JDBC TLS](https://learn.microsoft.com/en-us/sql/connect/jdbc/connecting-with-ssl-encryption?view=sql-server-ver17), [MariaDB TLS with pre-3.x distinctions](https://mariadb.com/docs/connectors/mariadb-connector-j/using-tls-ssl-with-mariadb-java-connector).

## Complete engine/dependency inventory

Every engine name links to its official driver page. Version, module, driver and transaction support are separate facts. “Module only” means the required vendor driver is absent in the approved distribution. Every row additionally inherits the command/edition/security gates below.

| Engine / source | Documented server versions or status | Module + JDBC/native dependency | Pinned dependency / DDL flag |
| --- | --- | --- | --- |
| [Aurora DSQL](https://documentation.red-gate.com/flyway/reference/database-driver-reference/aurora-dsql) | Latest compatible; verified N/A | dsql + AWS DSQL JDBC connector | Neither bundled; no loaded label / ? |
| [Aurora MySQL](https://documentation.red-gate.com/flyway/reference/database-driver-reference/aurora-mysql) | Latest; verified 5.5/8.0 | mysql + MySQL Connector/J | MariaDB fallback only / N |
| [Aurora PostgreSQL](https://documentation.red-gate.com/flyway/reference/database-driver-reference/aurora-postgresql) | Latest; verified 9.6/14.x/17.4 | postgresql + pgJDBC | Present / Y* |
| [Azure Synapse](https://documentation.red-gate.com/flyway/reference/database-driver-reference/azure-synapse) | Latest dedicated pools | sqlserver + Microsoft JDBC | Present / N |
| [Babelfish](https://documentation.red-gate.com/flyway/reference/database-driver-reference/babelfish) | Verified Aurora PostgreSQL 16.4 | sqlserver + Microsoft JDBC | Present / Y* inherited SQLServer |
| [Cassandra](https://documentation.red-gate.com/flyway/reference/database-driver-reference/cassandra-database) | Latest; JDBC verified N/A | cassandra + wrapper; native Cassandra alternative | Present, nested dependencies / N JDBC |
| [ClickHouse](https://documentation.red-gate.com/flyway/reference/database-driver-reference/clickhouse-database) | Latest; verified N/A | community clickhouse + clickhouse-jdbc | Module only / N |
| [CockroachDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/cockroachdb) | Table 21.1; verified 21.2/26.1 | cockroachdb + pgJDBC | Present / N |
| [CosmosDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/cosmosdb) | Verified Mongo API 7.0 | native MongoDB SDK; mongosh for JS | SDK present; mongosh not approved / type-specific |
| [Couchbase](https://documentation.red-gate.com/flyway/reference/database-driver-reference/couchbase) | Preview V7.6.6-N1QL | native Couchbase SDK | Present / native conditional |
| [CUBRID](https://documentation.red-gate.com/flyway/reference/database-driver-reference/cubrid) | Verified N/A; no umbrella range | community cubrid + cubrid-jdbc | Module only / N |
| [Databricks Lakehouse](https://documentation.red-gate.com/flyway/reference/database-driver-reference/databricks) | Unity Catalog; verified N/A | databricks + JDBC 3.4.2 | Present / N |
| [Db2 LUW](https://documentation.red-gate.com/flyway/reference/database-driver-reference/db2) | 9.7/9.8/10.1/10.5/11.1/11.5 | db2 + IBM jcc | Module only / Y* |
| [Db2 z/OS](https://documentation.red-gate.com/flyway/reference/database-driver-reference/db2-zos-database) | Latest; verified N/A | community db2zos + IBM jcc | Module only / Y* |
| [Derby](https://documentation.red-gate.com/flyway/reference/database-driver-reference/derby) | 10.11–10.15; verified endpoints | derby + derbyclient/embedded driver | Module only / Y* |
| [DocumentDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/documentdb) | Latest; verified docdb 5.0.0 | native MongoDB SDK; mongosh for JS | SDK present; mongosh not approved / JSON vs JS |
| [DuckDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/duckdb) | Verified N/A; no umbrella range | community duckdb + duckdb_jdbc | Module only / Y* |
| [Fabric Database](https://documentation.red-gate.com/flyway/reference/database-driver-reference/fabric-database) | Verified SQL Azure 12.0.2000.8 | sqlserver + Microsoft JDBC | Present via family / Y* family, unaccepted |
| [Fabric Data Warehouse](https://documentation.red-gate.com/flyway/reference/database-driver-reference/fabric-data-warehouse) | Driver page preview/latest; umbrella excludes | sqlserver + Microsoft JDBC | Present, loaded label / N |
| [Firebird](https://documentation.red-gate.com/flyway/reference/database-driver-reference/firebird) | 3.0/4.0 | firebird + Jaybird 3.0.10 | Present / Y* |
| [Google BigQuery](https://documentation.red-gate.com/flyway/reference/database-driver-reference/google-bigquery) | Latest | gcp-bigquery + Simba JDBC | Module only / N |
| [Google Cloud Spanner](https://documentation.red-gate.com/flyway/reference/database-driver-reference/google-cloud-spanner) | Latest, GoogleSQL only | gcp-spanner + cloud-spanner-jdbc | Present, nested deps / N |
| [H2](https://documentation.red-gate.com/flyway/reference/database-driver-reference/h2) | Table 1.2–1.4/2.0; verified 1.2/2.0 | core + H2 2.3.232 | Present / N |
| [HSQLDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/hsqldb) | Table 1.8/2.0/2.2/2.3/2.4; verified 2.7 | hsqldb + driver 2.7.2 | Present / N |
| [Ignite](https://documentation.red-gate.com/flyway/reference/database-driver-reference/ignite) | Compatible; verified N/A | community ignite + ignite-core | Module only / N |
| [Informix](https://documentation.red-gate.com/flyway/reference/database-driver-reference/informix) | 12.10 | informix + vendor JDBC | Module only / Y* |
| [InterSystems IRIS](https://documentation.red-gate.com/flyway/reference/database-driver-reference/iris) | Verified N/A; no umbrella range | community iris + intersystems-jdbc | Module only / Y* |
| [MariaDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/mariadb) | Table 5.1/5.2/5.3/5.5, 10, 10.1–10.6, 10.10/10.11; verified 5.1/12.3.2 | mysql + MariaDB 2.7.14 | Present / N inherited MySQL |
| [MongoDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/mongodb) | Latest; verified V5/V8 | native MongoDB SDK; mongosh for JS | SDK present; mongosh not approved / JSON vs JS |
| [MySQL](https://documentation.red-gate.com/flyway/reference/database-driver-reference/mysql) | 5.6/5.7/8.0/8.1/8.2/8.3/9.4 listed | mysql + Connector/J or documented MariaDB fallback | MariaDB only / N |
| [OceanBase](https://documentation.red-gate.com/flyway/reference/database-driver-reference/oceanbase-database) | Latest; verified N/A | community oceanbase + oceanbase-client | Module only / N inherited MySQL |
| [Oracle Database](https://documentation.red-gate.com/flyway/reference/database-driver-reference/oracle-database) | 19c/21c/23ai supported; ≤18c compatible; verified 18.4/26ai | oracle + ojdbc11 21.18.0.0 | Present / N |
| [Percona XtraDB Cluster](https://documentation.red-gate.com/flyway/reference/database-driver-reference/percona-xtradb-cluster) | 8; 5.7 foundational only | mysql + MySQL Connector/J | MariaDB fallback only despite page ships=yes / N |
| [PostgreSQL](https://documentation.red-gate.com/flyway/reference/database-driver-reference/postgresql-database) | 9–18 foundational; 12–18 advanced; verified 9.2/18 | postgresql + pgJDBC 42.7.14 | Present / Y* |
| [QuestDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/questdb) | Verified N/A; no umbrella range | community questdb + pgJDBC | Present / N |
| [Redshift](https://documentation.red-gate.com/flyway/reference/database-driver-reference/redshift) | Latest | redshift + Amazon JDBC | Module only / Y* |
| [SAP HANA / HANA Cloud](https://documentation.red-gate.com/flyway/reference/database-driver-reference/sap-hana-including-sap-hana-cloud) | On-prem 1.0/2.0; Cloud 4.0 | saphana + SAP ngdbc | Module only / N |
| [SingleStoreDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/singlestoredb) | Table rendered 7.87.1; verified 8.9 | singlestore + JDBC 1.2.8 | Present / N |
| [Snowflake](https://documentation.red-gate.com/flyway/reference/database-driver-reference/snowflake) | Service latest; verified 10.8 | snowflake + JDBC 4.3.4 | Present / N |
| [SQLite](https://documentation.red-gate.com/flyway/reference/database-driver-reference/sqlite) | 3.7+ 3.x; verified 3.0/3.7 | core/native SQLite + Xerial 3.50.3.0 | Present / Y* JDBC |
| [SQL Server](https://documentation.red-gate.com/flyway/reference/database-driver-reference/sql-server-database) | 2008/2008R2/2012/2014/2016/2017/2019/2022/2025 | sqlserver + Microsoft JDBC 12.10.2 | Present / Y* |
| [Sybase ASE](https://documentation.red-gate.com/flyway/reference/database-driver-reference/sybase-ase) | 15.7/16.0/16.1/16.2/16.3 | sybasease + jConnect or jTDS | jTDS 1.3.1 present; jConnect absent / N |
| [Testcontainers](https://documentation.red-gate.com/flyway/reference/database-driver-reference/testcontainers) | Underlying engine versions | wrapper + Testcontainers libraries + engine driver | Wrapper present; TC dependencies absent / underlying |
| [TiDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/tidb-titanium-db) | 5.0 | community tidb + MySQL Connector/J | MariaDB fallback needs proof / N inherited MySQL |
| [Timeplus](https://documentation.red-gate.com/flyway/reference/database-driver-reference/timeplus) | Verified N/A; no umbrella range | community timeplus + native JDBC | Module only / N |
| [TimescaleDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/timescaledb) | Table extension 2.13–2.16; driver labels 11/12 | PostgreSQL family + pgJDBC | Present via family / Y* family |
| [YugabyteDB](https://documentation.red-gate.com/flyway/reference/database-driver-reference/yugabytedb) | Table 2.4–2.7; verified 2.20/2.21/2024.1 | community yugabytedb + Yugabyte JDBC; PG alternative documented | Own driver absent; pgJDBC present / N |

## Per-engine scope, history, auth/TLS and verification

Foundational adapters retain history in an engine-specific table or collection equivalent. Every proposed adapter must prove exact location, locking, empty/populated history and visibility. No assumption that `schemas`, `defaultSchema` or multiple schemas work everywhere is safe.

The following shorthand describes **assessment profiles**, not implemented support:

- **OR**: Oracle password/transport; wallet/TCPS require separate acceptance.
- **PG**: PostgreSQL-family password/SCRAM and driver TLS; compatible wire protocol does not prove auth or schema equivalence on a variant.
- **MS**: Microsoft JDBC SQL password/TLS; integrated/Entra/Kerberos depend on endpoint and additional libraries/native configuration.
- **MY**: MySQL-family password/driver TLS; verify account host, auth plugin and driver version; host option files excluded.
- **J**: vendor JDBC credentials/TLS where supported. Exact authentication mechanisms and CA/hostname validation remain **unverified** until the linked vendor/driver documentation and live tests qualify them.
- **L**: embedded/local file authority; transport TLS not applicable. **J/L** adds a separately tested server mode.
- **CL**: service-specific token/key/IAM/password and TLS, requiring endpoint, credential lifecycle and trust qualification; no universal cloud authentication claim.
- **NC**: native SDK/endpoint credentials/TLS; Mongo-compatible projects distinguish JSON/JavaScript and external mongosh.

Every row inherits **G1–G6** below; engine-specific gates are added here. Unverified details are blockers to support, not default approvals.

| Engine | Scope/history location | Auth/TLS | Specific restriction / verification requirement |
| --- | --- | --- | --- |
| Oracle | Service/PDB/schema | OR | Preserve Free-lab boundaries and narrow recovery |
| PostgreSQL | Database/schema | PG | Priority contract above; declare one tested major |
| SQL Server | Catalog/schema | MS | Priority contract; login/user, collation, GO and transaction exceptions |
| MySQL | Database=schema | MY | Connector/J missing; qualify selected fallback and implicit commits |
| MariaDB | Database=schema | MY | Independent server/2.x-driver TLS and recovery tests |
| Aurora DSQL | Database/schema | CL | Missing module/driver; IAM/TLS, async DDL and locking |
| Aurora MySQL | Database=schema | MY | Aurora failover/provider permissions and fallback |
| Aurora PostgreSQL | Database/schema | PG | Provider version/role/lock behavior; not generic PG certification |
| Azure Synapse | Catalog/schema | MS | Dedicated pools only; foundational commands, no advanced claim |
| Babelfish | Catalog/schema via TDS | MS | Unsupported T-SQL and compatibility layer; not ordinary SQL Server |
| Cassandra | Keyspace/history table | J | JDBC deprecated; .cql admission, no DryRun; native alternative separately qualify |
| ClickHouse | Database/history table | J | Missing driver; community plugin and no verified server version |
| CockroachDB | Database/schema | PG | No PostgreSQL DDL-transaction equivalence; differing source versions |
| CosmosDB | Mongo database/history collection | NC | Mongo API only; JSON/JS transactions and service retry behavior; no DryRun |
| Couchbase | Bucket.scope/history collection | NC | Preview/cloud verification; one statement per migration; no DryRun; bucket creation/drop unsupported |
| CUBRID | Database/adapter schema | J | No verified version; missing driver; prove namespace/history behavior |
| Databricks Lakehouse | Catalog/schema | CL | Token/HTTP path, Arrow/JRE settings; advanced preview; not Lakebase |
| Db2 LUW | Database/schema | J | Missing IBM driver; CLP/CLPPlus utility commands not ordinary SQL |
| Db2 z/OS | Subsystem/location/schema | J | Missing driver; platform-specific SQL; community maintainer despite support-table label |
| Derby | Database/schema | J/L | Missing driver; JRE/client version and embedded path authority |
| DocumentDB | Database/history collection | NC | retryWrites=false observation, AWS CA setup, JSON-only transactions; no DryRun |
| DuckDB | File/catalog/schema | L | Missing native driver; host-file authority; no verified version |
| Fabric Database | Catalog/schema | MS | Foundational only; service endpoint/auth; family flag not live acceptance |
| Fabric Warehouse | Warehouse/schema | MS | Preview and umbrella-source conflict; no promotion |
| Firebird | Database/history table | J | No ordinary multi-schema model; history placement/transaction tests |
| BigQuery | Project/dataset/history table | CL | Simba missing; 10 GB Community limit across datasets; unattended browser OAuth unsupported |
| Cloud Spanner | Project/instance/database/history table | CL | GoogleSQL only; 10 GB Community limit; cloud credentials and async DDL |
| H2 | Database/schema | J/L | Embedded/server modes; no relationship to internal registry |
| HSQLDB | Database/schema | J/L | Source version mismatch; runtime does not advertise DDL rollback |
| Ignite | Cluster/cache/schema | J | Missing thin-client deps; no verified version |
| Informix | Database/schema/owner | J | Vendor driver absent; distribution and routine syntax |
| IRIS | Namespace/schema | J | Namespace not Oracle service; driver absent and version unverified |
| MongoDB/Atlas | Database/history collection | NC | No mixed JS/JSON project; JS no transactions, needs mongosh; no DryRun |
| OceanBase | Tenant/database/schema mode | J | Driver absent; mode semantics; no generic MySQL certification |
| Percona XtraDB | Database=schema | MY | Cluster lock/replication and fallback; documentation driver claim differs from pin |
| QuestDB | Database/default namespace | PG | Wire compatibility is not PostgreSQL schema or transaction support |
| Redshift | Database/schema | CL | Driver absent; external-table ALTER/DROP transaction detection caveat |
| SAP HANA/Cloud | Tenant database/schema | J/CL | SAP driver absent; tenant grants and quoted identifiers |
| SingleStoreDB | Database=schema | J | Source version formatting ambiguity; cluster/DDL behavior |
| Snowflake | Account/database/schema + warehouse/role | CL | Key material lifecycle, result format/JRE options; no parallel migration claim |
| SQLite | File/history table | L | No concurrent migrations or multi-schema support; host-file boundaries; never target Manager registry |
| Sybase ASE | Database/owner/history table | J | No flyway.schemas support, no DDL transactions; selected driver TLS/auth needs qualification |
| Testcontainers | Underlying engine | Underlying | Wrapper, not DBMS; Docker/socket/provisioning excluded |
| TiDB | Database=schema | MY | Driver fallback, distributed DDL/locking; community plugin |
| Timeplus | Database/stream namespace | J | Missing native JDBC; no verified version; history/stream semantics |
| TimescaleDB | Database/schema + extension | PG | Version labels ambiguous; qualify both PG server and extension |
| YugabyteDB | Database/schema | PG | Explicitly select driver; no PostgreSQL transaction equivalence |

## Other named service/product variants

Aliases below appear in official support/feature material. Follow their family driver page and the [support table](https://documentation.red-gate.com/flyway/getting-started-with-flyway/system-requirements/supported-databases-and-versions); they inherit no live Manager acceptance. Service “Latest” is not an immutable version.

| Variant | Base connection/module and scope | Additional qualification |
| --- | --- | --- |
| Azure SQL Database; Azure SQL Managed Instance; RDS SQL Server; SQL Server on Google Cloud | SQL Server catalog/schema | Provider authentication/TLS/grants; Google versions listed 2017/2019/2022 |
| Oracle Autonomous; RAC/Exadata; RDS Oracle | Oracle service/PDB/schema | Wallet/TCPS and service identity; provider/RAC behavior; no physical Exadata claim |
| RDS PostgreSQL; Cloud SQL PostgreSQL; Azure PostgreSQL Flexible/Single Server | PostgreSQL database/schema | Provider auth/CA/privileges; a listed retired service is not a recommendation to provision it |
| EnterpriseDB Advanced/Extended Server | PostgreSQL family | Advanced Server 16 listed; Extended advanced preview, version unspecified; dialect/role acceptance |
| Databricks Lakebase; Snowflake Postgres | PostgreSQL-compatible | Separate service credentials/version; advanced preview; distinct from warehouse/Snowflake SQL adapters |
| RDS MySQL; Azure MySQL; Cloud SQL MySQL; Percona Server for MySQL | MySQL database/schema | Fallback/provider permissions; Percona Server 8 listed |
| SkySQL; RDS MariaDB | MariaDB database/schema | Service version/CA/account restrictions |
| MongoDB Atlas | Native MongoDB | Atlas network/auth/TLS and JSON/JS distinction |
| SAP HANA Cloud | HANA tenant/schema | Cloud 4.0; already included in primary catalog row |

## Edition, command and verification gates

**G1 — Version/dependencies.** Declare exact server version, module/driver hashes, JRE/native tools and edition. Missing dependencies require a separate approved manifest decision, never runtime jar downloads. Newer docs do not upgrade this pin.

**G2 — Auth/TLS.** Verify actual user/database identity, credentials, account mappings, trusted CA, wrong hostname, unknown CA and unavailable endpoint. No TLS downgrade, secret argv/logs or unrestricted JDBC properties.

**G3 — Scope/history.** Prove database/catalog/namespace, case/quoting, visible/hidden schemas, least privilege and empty/populated history. No implicit baseline, creation or separate history schema in the first slice.

**G4 — Commands/output.** Real Info/Validate/Migrate JSON/exit/error evidence, repeatables, checksum mismatch, failed history and unknown states. Baseline/Clean/Undo/general Repair remain unavailable even when supported upstream. Advanced comparisons cover fewer engines/versions than foundational migration commands.

**G5 — Transactions/recovery.** Compare actual committed/rolled-back effects and history, locks, timeouts/restarts/concurrency. Keep failed/ambiguous runs held and no implicit SQL retries. True DDL support never promises whole-run atomicity; Oracle repair policy is not generic.

**G6 — Product/security.** ID-scoped API/browser RBAC/PROD checks, credential redaction, immutable snapshots/logs, upgrade preservation and no process leaks. No operational engine label before all declared gates pass.

Foundational Info/Validate/Migrate/Baseline/Repair/Clean are Community commands; Undo/DryRun require Teams, and comparisons/drift/snapshots generally Enterprise plus engine support. BigQuery/Spanner have documented Community data limits; Oracle SQL*Plus and MySQL option files have paid restrictions. [Edition matrix](flyway-edition-feature-matrix.md) records the distinctions. No paid feature was tested.

## Source conflicts and explicit limits

- Fabric Warehouse's newer driver page says preview; the umbrella table excludes it; the pin loads it. Keep preview/unaccepted status.
- Aurora DSQL is documented but lacks the module/driver and loaded label. pgJDBC presence alone is insufficient.
- Oracle 26ai and MariaDB 12.3.2 appear in driver verification while broader ranges differ. HSQLDB, CockroachDB, SingleStore, TimescaleDB and YugabyteDB also have differing/ambiguous labels. Preserve both meanings; no invented merged range.
- Community-maintained modules may ship with no driver or verified version. Missing versions/auth/TLS details stay unverified until a specific engine is selected for qualification.
- BigQuery's driver table says SSL “No” while showing HTTPS; Couchbase says “No” while showing couchbases. Neither justifies a plaintext/certificate-verification claim. Consult SDK/vendor evidence before enablement.
- Native Oracle/Cassandra/SQLite subpages describe alternative connectors, not extra DBMS. Accepted Oracle uses JDBC; no connector-mode change is recommended.
- Testcontainers is a wrapper; SQLite migration-target support does not authorize Flyway against Manager's internal SQLite database.

The [source index](evidence/official-source-index.json) retains page dates, URLs and metadata hashes for all 47 driver entries. The breadth of this catalog does not change the [recommended scope](proposed-product-scope.md): Oracle preservation and one qualified PostgreSQL major first.
