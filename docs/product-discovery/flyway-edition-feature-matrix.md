# Edition and feature boundaries — Flyway 13.10.3

Assessed 2026-10-17. The verified local runtime reports **Flyway Community Edition 13.10.3** in [version output](evidence/version.txt) and `edition: COMMUNITY` in [actual metadata JSON](evidence/list-engines-json.txt). This establishes the assessment's free-edition baseline, not an audit of every possible external commercial subscription. No licensing command, trial, commercial operation or entitlement probe was executed.

Evidence hierarchy: actual pin and help establish what is shipped/exposed; explicit help tier markers and official command pages establish stated licensing; official driver pages establish engine restrictions; existing acceptance establishes Manager behavior. A jar, a help entry or a documented feature is not proof of licensed usability or live engine acceptance. Commercial entitlement must be approved separately before future testing.

This is the **Redgate CLI distribution running Community**, not an assumption that the open-source Maven artifacts expose an identical CLI surface. The [release notes](https://documentation.red-gate.com/flyway/release-notes-and-older-versions/release-notes-for-flyway-engine) explicitly distinguish OSS/Redgate behavior for Init. Database server editions (Oracle XE/Free, SQL Server editions, etc.) are another independent axis; free server licensing does not grant paid Flyway features.

## Entitlement matrix

✓ = documented included; — = not included at that tier; conditional = dependency/engine/feature-specific. These are assessed documentation statements, not legal entitlement grants.

| Capability | Community/free | Teams | Enterprise | Actual pin / Manager consequence |
| --- | --- | --- | --- | --- |
| Info, Validate, Migrate, Repair, Baseline, Clean | ✓ | ✓ | ✓ | All appear in help; Manager permits only first four and constrains Repair. No Baseline/Clean API |
| Versioned SQL and repeatables | ✓ | ✓ | ✓ | Manager supports V/R naming and immutable SQL packages; engine SQL acceptance remains separate |
| Java/script migrations, custom resolution | Documented, with callback limits | ✓ | ✓ | Excluded from Manager artifact admission and private process policy |
| Baseline migration files (`B`) | ✓ | ✓ | ✓ | Not the same as `baseline`; Manager normalizer admits only V/R. Defer adoption |
| Placeholders | ✓ | ✓ | ✓ | Available in pin, disabled in Manager; bounded non-secret configuration is P2 |
| Callbacks | Limited | ✓ | ✓ | Disabled by generated config plus file admission; no future entitlement should weaken this implicitly |
| JSON machine-readable output | ✓ | ✓ | ✓ | Observed metadata JSON; existing migration parsing retained |
| Undo / `U` migration scripts | — | ✓ | ✓ | Explicit Teams help markers; repeatables have no Undo. P3 |
| `cherryPick`, `dryRunOutput`, stream/batch, error overrides | — per pinned help | ✓ | ✓ | Do not expose under Community. No execution trial performed |
| `detectEncoding` | **Conflicting sources** | ✓ | ✓ | Pinned help says Teams; current config reference says Community. Keep unexposed pending pin-specific clarification |
| SQLFluff code review | ✓, dependency required | ✓ | ✓ | Pin reports **not installed**; no working code-review capability claim |
| Fail CI on SQLFluff violations | — per feature summary | ✓ | ✓ | Help exposes `check.code.failOnError` without an entitlement label; not evidence of free gating |
| Custom regex code rules / commercial code review | — | — | ✓ | P3; future CI integration requires licensed acceptance |
| Check DryRun | — | ✓ | ✓ | Explicit Teams flag, with engine restrictions |
| Check Changes / Drift | — | — | ✓ | Explicit Enterprise flags; may rebuild/clean shadow targets |
| CLI Diff, DiffText, Generate, Model, Prepare, Deploy | — | Not assumed for CLI automation | ✓ in command index | Teams Desktop workflow availability must not be substituted for CLI entitlement |
| Snapshot | — | — | ✓ | Explicit Enterprise help; can write snapshot-history rows |
| `objects` | Unresolved | Unresolved | Unresolved for standalone operation | Exposed in pin, absent from command-tier index. Comparison companion; excluded until clarified |
| `testConnection` | Unresolved standalone tier | Unresolved | Unresolved | Help exposed; no DB execution. New Manager discovery uses a fixed-query adapter, not unrestricted environments |
| Init, Add, List Engines, Help/Version | ✓ (command index/help) | ✓ | ✓ | Metadata/local authoring. Manager does not become a file authoring tool |
| Auth | License administration | Can start Teams trial | Enterprise flow documented | `auth` help is available in Community; availability is not a commercial entitlement |
| MCP | — in current command index | Not assumed | ✓, preview | Pin advertises command; `help mcp` errors. No server started |
| External secrets integrations / advanced provisioning | Not assumed | Not assumed | Feature/adapter dependent | Preserve current encrypted local vault. Do not enable environment resolvers because integration jars ship |
| Supported older database versions | Current v10+ policy removes old-server license requirement | Support subscription differs | Support subscription differs | Database compatibility, vendor certification and commercial support are separate axes |
| Redistribution rights | Not established by this assessment | Not established | Distribution add-on described | Preserve existing approved runtime process; a commercial redistribution decision needs its own review |

Sources: [command tiers](https://documentation.red-gate.com/flyway/reference/commands), [feature summary](https://documentation.red-gate.com/flyway/learn-more-about-flyway/feature-summary), [configuration tier labels](https://documentation.red-gate.com/flyway/reference/configuration/flyway-namespace), and [pinned help](evidence/help.txt). The feature summary includes Desktop, CLI, planned and preview features; it must be read in that context. Features marked “coming soon” (for example automatic repair and some verification/tag capabilities) are **not** current capabilities. Manager's existing narrow Oracle repair is application policy, not a claim to Flyway's advertised future auto-repair feature.

## Engine-specific restrictions

| Engine / capability | Edition and dependency finding | Manager decision |
| --- | --- | --- |
| Oracle SQL*Plus emulation | [Oracle page](https://documentation.red-gate.com/flyway/reference/database-driver-reference/oracle-database) labels it Teams; opt-in `oracle.sqlplus` | Keep false. Ordinary JDBC SQL/PLSQL acceptance does not imply SQL*Plus support |
| PostgreSQL password/SCRAM | [PostgreSQL reference](https://documentation.red-gate.com/flyway/reference/database-driver-reference/postgresql-database) describes pgJDBC authentication; feature summary places plain/SCRAM in Community | P1 password credential flow; pgpass/Kerberos/cloud IAM excluded initially |
| MySQL option files | [MySQL reference](https://documentation.red-gate.com/flyway/reference/database-driver-reference/mysql) labels option-file credential retrieval Teams | Reuse encrypted credentials; never read host option files |
| BigQuery | [Driver reference](https://documentation.red-gate.com/flyway/reference/database-driver-reference/google-bigquery) states Community **10 GB across datasets**, paid tiers remove limit; Simba JDBC absent | P3 catalog only. No free unlimited-data claim |
| Cloud Spanner | [Driver reference](https://documentation.red-gate.com/flyway/reference/database-driver-reference/google-cloud-spanner) states Community **10 GB database limit**; only GoogleSQL dialect | P3; cloud credentials and batching require independent design |
| MongoDB / CosmosDB / DocumentDB | Native connector path; JSON versus JavaScript changes dependencies/transactions; no DryRun on documented Mongo-compatible path | P3. SQL-only Manager cannot support these by changing an engine dropdown |
| Couchbase | Foundational support preview; one statement per migration; no DryRun; native connector only | P3; do not market as accepted support |
| Comparison/advanced capabilities | Engine/version subset of foundational support, especially SQL Server/Oracle/PostgreSQL/MySQL families | Do not infer diff/drift support for every engine or older server |
| Community-maintained plugins | Some ship in pin but drivers do not; no verified server version on several pages | Not a paid/free distinction by itself. Qualification and dependency approval remain mandatory |

See the [full database catalog](flyway-database-support-matrix.md) for per-engine details and source conflicts. Current support labels do not override the pin: Fabric Warehouse preview and Aurora DSQL are examples where documentation presence does not establish an accepted runtime path.

## Admission rule for a future capability

A capability is eligible only when **pin exposes it ∩ dependencies verified ∩ edition permits it ∩ database/version qualified ∩ Manager policy permits it ∩ caller authorized**. Store these as distinct diagnostics. Licensing success must never enable Clean, arbitrary configuration or a new database engine automatically. No license, runtime or database changes are part of Phase 6.
