# Incremental implementation plan and acceptance gates

Proposal dated **2026-10-17**. Phase 6 ends with this decision package; implementation, database tests and rollout require a subsequent authorized task. Keep Flyway **13.10.3**, Go monolith, SQLite and the existing execution/credential architecture. Recommended scope: **PostgreSQL first**, initially one declared major (proposed 17), password/SCRAM, verified TLS, existing lowercase unquoted database/schema/role identifiers, same-schema history and immutable V/R SQL.

## Comparison and sequencing rationale

| Option | Reuse | New work / testing effort | Dependency/distribution issue | Decision |
| --- | --- | --- | --- | --- |
| PostgreSQL first | Already pinned pgJDBC/module, vault, artifacts, Info UI, queue, grants, history/logs | Database/schema profile, fixed-query discovery, lowercase IDs, transactional failure policy and advisory-lock exceptions | No runtime change expected | Smallest useful second-engine proof; first |
| SQL Server first | Already pinned Microsoft JDBC/module and execution envelope | Catalog/login/user/schema and collation; GO batches, TLS and transaction exceptions; integrated/cloud identity much larger if included | SQL password needs no new driver; advanced auth dependencies not assumed | Second unless a concrete Microsoft workload changes priority |
| MySQL/MariaDB first | Shared mysql module, existing MariaDB driver; Oracle partial-DDL operational concepts | Database=schema UI/model, OS/server case behavior, user@host grants, DELIMITER and nontransactional recovery; two independent server suites | MySQL Connector/J absent; MariaDB fallback has compatibility caveats | MariaDB can qualify independently later; do not promise MySQL from a shared module |

Effort comparisons are qualitative architectural judgment. No business workload inventory, time estimate or commitment was supplied. A different business priority can reorder engines after preserving the same safety gates. Do not turn the full upstream catalog into a commitment to build every adapter.

## P0 — Preserve the accepted Oracle product

Before any implementation, inventory current tracked/untracked changes and reuse the existing [acceptance baseline](../projects-flyway-info-acceptance.md). Freeze relevant v7 API fixtures, immutable snapshot/config bytes, known Oracle error behavior and runtime pin. Scope changes only to the agreed engine boundary.

**Gate A:** Oracle behavior remains equivalent: connect/discover/save, schema/account mappings, original credential bytes, grants/PROD rules, projects/current-member history, catalog/deep links, Info latest-failure/historical semantics, immutable V/R artifacts, async execution, constrained repair, restart/timeout holds and no implicit retry. Existing Oracle fingerprints must not change because an `engine` default was added. No Oracle account, data or runtime modification is needed for this local contract gate.

## P1a — Profile/model compatibility

Implement the [minimal profile and v7 evolution](multi-database-reference-architecture.md): explicit engine and database, engine-aware endpoint uniqueness, profile/TLS revision and adapter selection; keep old IDs and grants. Do not add a Database parent entity. Add tagged engine-aware REST contracts while retaining legacy Oracle input/response behavior.

**Gate B:** populated-v7 disposable backup restore upgrades transactionally with all original rows, credential ciphertext, installation identity, artifacts, run/execution/log/audit/capture bytes and foreign-key references preserved. Verify migration checksums, rollback on invalid data, custom object handling, integrity and foreign-key enforcement restoration. Two PostgreSQL databases on one host/port coexist; Oracle duplicates remain rejected. New engine is immutable, unknown engines fail closed, old Oracle payloads still work. No arbitrary URLs/options or fake Oracle service values.

The required SQLite migration is a **future deliverable**, not an output of Phase 6. Its acceptance must not modify the live registry. An older binary must refuse the newer schema; a downgrade is not the rollback plan.

## P1b — PostgreSQL connection, discovery and native Info

This is the **smallest next engineering increment**. Implement the PostgreSQL fixed-query JDBC adapter using the approved driver/JRE, metadata protocol, time/resource bounds and private credential transport. Add PostgreSQL fields to the existing wizard and database-qualified destination labels. Save re-probes and atomically records the selected existing schema/account mappings and allowlists. Reuse recorded/refresh Info, including artifact selection, warnings, raw diagnostics and historical context. Keep PostgreSQL Migrate disabled at this gate.

**Gate C — connection/scope/TLS:** use a specifically authorized disposable PostgreSQL lab/version in the later implementation phase. Prove correct/wrong password, SCRAM, unavailable host, wrong database, actual database/user identity, wrong hostname, unknown CA and valid CA. Verify private CA staging without changing the runtime manifest, minimal environment and no credential leakage. Discovery performs SELECT/metadata only; inaccessible/system schemas are excluded or classified appropriately. No DDL probe, role escalation or schema creation. Same-named schemas in two databases remain distinct; hidden schemas never reach schema-only readers. Unsupported quoted/case-sensitive/overlength names fail with explicit scope limits.

**Gate D — native Info:** actual pinned CLI JSON over empty and populated history with declared immutable artifacts; history table absent, missing/partial/malformed results, artifact-relative Pending/Missing/Future, unknown native state, wrong database identity and changed profile/trust material all produce explicit evidence. Compare before/after objects, data and history to prove the inspection did not mutate the database. Confirm refresh creates no migration job, stream reservation or auto-baseline. Viewers see authorized recorded evidence, no account-administration requests and no refresh; Operators need operate plus explicit PROD authority. Existing Oracle acceptance contract still passes.

If the pinned runtime cannot satisfy the chosen profile/TLS/Info behavior, leave this engine unavailable and raise a separate compatibility decision. Do not patch jars, disable TLS verification or silently fall back to another connector.

## P1c — PostgreSQL migration execution

After C/D pass, connect the engine-aware plan to the existing preflight/queue/worker and historical UI. Native Info and Validate run before migration and verify it afterward. Keep no new command, artifact type, SQL rewrite or free-form setting. PostgreSQL automatic Repair remains disabled.

**Gate E — full migration and failure behavior:**

- Real V1/V2 migrations and a repeatable complete successfully in the declared schema/history, retaining exact artifact/runtime/profile/account evidence. Validate and post-Info agree; a second identical submission does not replay SQL under idempotency rules.
- Same database/different schemas and same endpoint/different databases keep artifacts, locks, permissions, history and destination identity separate. Schema grants do not falsely promise protection from explicit cross-schema SQL; lab accounts demonstrate least privilege.
- Transactional failure records the actual rollback and held failed run, without Repair or retry. A run with an earlier committed migration and later failure does not claim whole-run rollback. Nontransactional/concurrent-index examples are blocked or end in explicit unsupported/held evidence, never an accepted rollback guarantee. SQL-classifier coverage limits remain documented.
- Corrupted checksums, missing/Future/Failed history, changed profile/runtime, inactive/changed account, revoked grant, held/busy stream, predecessor failure, malformed/truncated output, timeout, restart and process termination fail closed. No failed migration becomes an implicit retry through a later upload. No claim that a terminated client cancels already committed SQL.
- Keep Oracle's eligible one-row failed-history repair tests and repeatable/checksum/deletion exclusions. PostgreSQL failure never enters that branch. Successful history cannot be checksum-realigned under a new engine policy.

The first PostgreSQL release deliberately holds all failed/ambiguous migrations for review. There is no generic reconciliation/Retry API today. Document operator escalation and preserve evidence; do not ship a UI promise of recovery or instruct manual registry edits. A new safe reconciliation action is a separately scoped follow-up if actual operational demand requires it.

**Gate F — complete product acceptance:** existing Go unit/race, frontend, browser, security and build checks plus meaningful new adapter/model tests pass. Real browser tests exercise create connection → discover/select schema → upload → preflight → queue → completion → history → native Info/logs, including canonical navigation, refresh/Back, mobile layouts, Admin/Operator/Viewer, scoped service token and PROD denial. Review OpenAPI compatibility and credential scanning of evidence. Run tests against disposable data, not the live homelab.

Only after A–F pass may the UI advertise that **specific PostgreSQL version/profile** as operational. A successful connection test or fixture-only browser result is insufficient.

## P1d — Later authorized release gate

Prepare a concrete reviewable source diff, CI result, immutable image identity, backup/restore rehearsal and operator acceptance package. A release requires explicit authorization to push/publish/deploy, the established GitHub Actions/GitOps path and a consistent registry plus separately protected key backup. Confirm live installation identity/data preservation afterward. Do not roll an older binary over upgraded SQLite. Phase 6 authorizes none of these external actions.

## P2 — Bounded optional work

| Increment | Prerequisite / gate | Reason to defer |
| --- | --- | --- |
| Standalone recorded Validate | Reuse operate/PROD, immutable artifact/profile and append-only evidence; real mismatch/empty/failure acceptance | Pipeline already validates; not essential to second-engine usefulness |
| Non-secret placeholders | Bounded keys/values, literal handling, immutable effective config/hash and engine quoting tests; no resolver or secret expansion | SQL substitution can change semantics and leak values; never a generic config editor |
| PostgreSQL nontransactional migrations | Explicit script-config admission policy, transactional/session advisory-lock tests, concurrency/failure/restart and recovery evidence | Concurrent indexes need more than a boolean setting; arbitrary `.conf` files remain excluded |
| Additional PostgreSQL majors | Repeat declared dependency/server compatibility and real command/failure acceptance | Upstream range is not Manager acceptance |
| SQL Server | New adapter, catalog/collation identity, SQL password/TLS and GO/transaction gates; exact version declared | Separate engine, not a profile-only change |
| MariaDB, then MySQL if needed | DB=schema/case/account-host adapter; implicit-commit failure; version-specific TLS/driver tests | MariaDB acceptance cannot certify MySQL. If fallback fails, stop and separately review runtime dependencies |
| Catalog scaling / external lint reports | Demonstrated size/latency need or a qualified external CI report source | Avoid adding infrastructure or SQLFluff dependencies without demand |

## P3 — Keep excluded

Clean, general Repair, Undo, uncontrolled Baseline/adoption, skip/mark/cherry-pick/out-of-order histories, Java/script callbacks, arbitrary JDBC/TOML/locations, cloud provisioning/Testcontainers in the server, MCP and commercial state/diff/drift/generate/prepare/deploy workflows. A licensed tier does not authorize these. Reconsider only with a separate business case, explicit policy and acceptance design.

## Decision and remaining uncertainty

Approve scope conceptually as Oracle preservation plus PostgreSQL, with **P1a/P1b** the next implementation slice and Migrate gated behind P1c. Outstanding implementation evidence is intentionally explicit: PostgreSQL lab/version, fixed-query privilege classifier, verified private-CA handling, parser/nontransactional limitations and end-to-end failure behavior. These require future engineering and authorized disposable database tests, not more generic feature cataloging.

The [CLI inventory](flyway-cli-capability-catalog.md), [complete database catalog](flyway-database-support-matrix.md), [edition matrix](flyway-edition-feature-matrix.md), [gap analysis](current-product-gap-analysis.md) and [reference architecture](multi-database-reference-architecture.md) supply the factual basis and traceability. Phase 6's completion means this decision package is complete; it does not mean a second engine has been implemented or released.
