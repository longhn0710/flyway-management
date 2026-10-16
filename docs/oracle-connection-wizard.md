# Oracle connection wizard verification

The guided workflow uses the existing target, schema, execution-account, encrypted credential and Flyway execution resources. Test performs only fixed Oracle metadata SELECTs. Save repeats discovery and commits registration, selected mappings and any explicitly selected application grants atomically. Each execution account has its own allowlist; another configured account is preserved. Removing all mappings on an existing account is supported. Schema identities, immutable SQL, runs, logs and audit remain intact.

The normal migration picker uses operational APIs with RBAC, PROD authorization, activity, configured credentials and account allowlists applied before pagination. Registry administration retains advanced multi-account operations. Direct API attempts to add a mapping to a wizard-managed account cannot bypass its allowlist. New submission, preflight, worker dispatch and automatic repair check current authorization. Completed idempotent submissions resolve to their original historical run.

## Oracle evidence

Acceptance reused the existing `oracle-free` StatefulSet in `flyway-manager-oracle-lab`, service/PDB `FREEPDB1`. No new database, NetworkPolicy expansion, unrelated schema change or implicit Oracle grant was introduced by the product. Explicit fixture setup created only dedicated lab users/schema owners. Discovery metadata includes every visible user and accessible object/schema-privilege owner, with no schema-row cap. Oracle-maintained and quoted identifiers remain visible and counted but cannot be selected.

| Account | Discovered | Potential | Unknown | Unsupported |
| --- | ---: | ---: | ---: | ---: |
| FWM_LAB (limited application account) | 46 | 1 | 16 | 29 |
| FWM_WZ260_VIEW (visibility exceeds DDL rights) | 46 | 0 | 16 | 30 |
| FWM_WZ260_NONE (no supported new stream) | 46 | 0 | 16 | 30 |
| FWM_WZ260_MULTI (schema-scoped DDL evidence) | 46 | 2 | 14 | 30 |
| FWM_WZ260_A (dedicated own-schema migration) | 46 | 1 | 16 | 29 |

Counts matched the accessible metadata union. For MULTI, only A/B had relevant `CREATE ANY TABLE` schema-grant evidence; neither was represented as fully verified. The own-schema A account had `CREATE TABLE`, a bounded quota and no broad system DDL grants. Unknown selections require an explicit acknowledgement. No DDL probe, schema creation or grant is executed during Test/Save.

The real Chromium workflow authenticated, discovered all 46 candidates, defaulted only the appropriate own schema, explicitly granted the current application user scoped access, saved, uploaded immutable SQL, passed live preflight and completed one controlled V1 migration in the initially empty dedicated schema A. Run `mbnbuUylHj9OHY8kiWBe0k` succeeded and exposed 11 centralized log entries. The full-width Flyway Info table, collapsed diagnostics, destination confirmation and run progress were checked through the actual UI.

Direct API acceptance additionally checks an unselected B even with broader Oracle DDL evidence, an application operate grant and an account mapping; adding/removing mappings, removing all mappings, preserving the prior run and another account, credential rotation, invalid credentials, unavailable network, a denied application Operator and an account with no potential target. Sanitized evidence is recorded in `docs/evidence/oracle-connection-wizard-20261016.json`.

Before migration, independent SQL observed the existing FWM_LAB V1–V4 history, four probe rows, unchanged guard canary and zero objects in new schema A. Only dedicated schema A's approved acceptance migrations are submitted by this workflow.

## Checks and operating boundaries

Established build/vet/unit/integration, race, frontend lint/unit, Chromium and vulnerability checks are required. The browser suite has 15 cases. Tests cover atomic rollback, credential version races, per-account authorization, pagination beyond 200 schemas, immutable history, queued revocation, repair revocation, schema-3 to schema-4 data preservation, partial metadata, output limits and case-sensitive discovery identities. OpenAPI recommended validation passes with the two existing public-route response warnings and established style exclusions.

The probe embeds a small fixed-query Java helper and uses only the manifest-verified bundled Java/JDBC distribution. Helper source is `internal/oracle/OracleJdbcProbe.java`; the class is built with JDK 21 using `javac --release 11 -proc:none -g:none` and embedded from `internal/flyway/OracleJdbcProbe.class`. Tests verify SHA-256 `0ba58c363c440c9432d84643663258d28532ec1bed41aac8801dfdc6df1f27c8` and reproduce it when the pinned compiler is available. Credentials use stdin, never process arguments, environment variables or persistent helper files. Process concurrency, deadlines, individual reads, Java heap and metadata bytes are bounded. The 16 MiB metadata transport budget yields explicit partial-result warnings; unfinished views stay unknown. There is no arbitrary schema-count truncation.

The unchanged approved Flyway runtime contains JDBC 21.18. Its inherited client compatibility limits remain: a valid Oracle 23/26 40-byte password and a 30-byte whitespace/Unicode fixture authenticated with SQL*Plus but were rejected by this driver. The probe preserves the bytes and explains compatibility when applicable; it cannot promise usable migrations with credentials the unchanged migration driver rejects. No unapproved JDBC replacement was added. Live TCPS, Oracle 19c/21c, production and cross-schema migration DDL remain unverified; metadata evidence is not a general migration authorization proof.

SQLite migration 004 is additive and preserves the existing installation/key and history. Older schema-3 binaries cannot read the upgraded ledger; the quiet-window database backup and separately stored original key are required before promotion. Delivery uses the established CI-published immutable image and GitOps chart promotion described in `homelab-operations.md`.
