# Reviewed Stitch frontend implementation

Implementation for review; no commit, push, merge, publication, deployment or Kubernetes resource change was performed. The implementation request authorizes the frontend work proposed by the previously preserved design package. Its historical approval statements and exports remain unchanged.

Design source: [Stitch project 9773888998249082148](https://stitch.withgoogle.com/projects/9773888998249082148), using the current reviewed screens. The pre-existing design-inspection archive is preserved locally and excluded from this implementation release. Existing screens were retrieved read-only; no design was generated or refined.

## Reviewable stages

1. **Shell and shared components.** Inter typography, approved teal/neutral tokens, Home → Instances → Databases / Schemas → Run Migration → Migration History navigation, contextual Administration, responsive menu, English labels, keyboard focus and session controls. Removed the phase/implementation banner from the product UI.
2. **Instances and connections.** Searchable/filterable/paginated registry, instance overview/schema/connections/history tabs, global catalog scoped to an authorized instance, registry activity distinct from connection health. Connections load real per-instance accounts and write-only credential metadata. Account edits bind to the selected account. Registration has Details → Test/discovery → Allowed scope → Review/save; only the final step persists. Unknown capability acknowledgement, unsupported-schema exclusion, independent current-user/PROD grant choices and fresh atomic save remain enforced. Discard confirmation clears transient passwords. Account lifecycle/revocation remains available; allowlist edits and credential rotation use the discovery/review workflow.
3. **Migration and evidence.** Instance/schema links preselect the grants-aware destination; inaccessible explicit destinations stay unselected. The Oracle engine, environment, instance, endpoint/service, schema and execution account are visible with a three-stage migration indicator. Existing immutable uploads, manifests, preflight tuple invalidation, unchecked explicit confirmation, idempotency, queued-only cancellation and separate migration/repair outcomes remain intact. Polling logs support channel/search/page controls, follow/pause, refresh time and connection-loss explanation.
4. **Administration and verification.** Existing user grants, service account/token issuance/revocation/rotation, authentication settings and final-Admin safeguards remain on their real APIs. Browser regressions exercise the new navigation and account context. Responsive and persisted Oracle evidence screenshots accompany the checks below.

Each stage was typechecked before dependent integration. Lint/unit and browser checks were repeated after integration changes. See final results below for the completed working tree.

## Compatibility and safety

No Go source, CLI implementation, SQLite schema, existing migration SQL, credential encryption, RBAC policy or backend allowlist logic was changed. Old `/databases` links redirect to Instances; `/databases/:id` retains instance/schema context. `/execution-accounts` redirects to Instances because credentials are contextual to Connections. Existing `/runs` and administration URLs remain usable. Login continues to preserve safe deep links and server-managed cookie/CSRF behavior.

Oracle remains the only operational adapter. PostgreSQL/MySQL/SQL Server are explicitly future options, with disabled engine-specific previews and no test/discover/save/preflight/execute requests. Engine labels identify the known Oracle backend baseline; this is not a new persisted engine contract.

## Test and browser evidence

The exact established command `make check test-race test-e2e security` passed: production build, Go vet/tests/race, ESLint, 16 frontend unit tests and all 15 browser tests, npm audit (zero vulnerabilities), and govulncheck (zero reachable/imported-package vulnerabilities; one advisory in an unused required module). See [the full CI log](evidence/stitch-implementation/ci.log). Vite retains a non-failing JavaScript chunk-size advisory; deployment/image publication jobs were intentionally not run. Backend verification and Oracle provenance: [detailed checks](evidence/stitch-implementation-checks.txt). The browser captures use a private SQLite online backup of the existing lab manager data and the actual Go APIs; original data/key files were not modified. Synthetic browser cases remain clearly identified as isolated controlled-Flyway or intercepted discovery regression tests, rather than Oracle execution evidence.

- [Desktop instances](evidence/stitch-implementation/real-oracle-instances-1440.png)
- [Instance overview](evidence/stitch-implementation/real-oracle-overview-1440.png)
- [Connections](evidence/stitch-implementation/real-oracle-connections-1440.png)
- [Migration destination](evidence/stitch-implementation/real-oracle-migration-1440.png)
- [Persisted run and logs](evidence/stitch-implementation/real-oracle-run-details-1440.png)
- [Mobile instance overview](evidence/stitch-implementation/real-oracle-overview-390.png)
- [Mobile run and logs](evidence/stitch-implementation/real-oracle-run-details-390.png)
- [Responsive browser measurements](evidence/stitch-implementation/real-browser-checks.json)

Desktop/tablet/mobile checks cover 390, 768, 1024 and 1440 px. Tables and logs scroll locally; page-level horizontal overflow and uncaught rendering errors are checked. Recorded keyboard/modal checks verify focus stays in the dialog, Escape requests discard, Keep editing preserves inputs, and discard restores focus. Browser regressions verify forward/back navigation.

## Real Oracle preflight

Existing successful run `q0GHoTkZokkhjB2KF0_SJF` and its stored `VERIFIED` preflight, artifact manifest, history and 11 logs remain readable. Fresh checks use the correct `flyway-manager-oracle-lab/oracle-free` service and existing FWM_LAB schema through a temporary loopback tunnel. Two initial authentication errors targeted a different namespace and are excluded as credential evidence. Correcting the tunnel verified connectivity/authentication; live preflight then refused the old artifact with `HISTORY_UNSAFE`. No migration was queued, no repair was requested and no existing Oracle SQL/history was modified. [Live browser diagnostics](evidence/stitch-implementation/real-oracle-preflight.json) show network/authentication/service VERIFIED and the already-applied V3/V4 reported as Future because they are absent from the V1/V2 package. [Desktop](evidence/stitch-implementation/real-oracle-preflight-1440.png) and [mobile](evidence/stitch-implementation/real-oracle-preflight-390.png) captures record this refusal with zero queue requests. A second [existing wizard artifact check](evidence/stitch-implementation/real-oracle-wizard-preflight.json) also verified connectivity/authentication and refused Future history. Preserving these refusals is not permission to repair the lab.

**Live acceptance limitation:** no new ready=true Oracle preflight was established with the available historical artifacts. The original V4 bytes were unavailable in the local acceptance material checked; no migration was reconstructed. A complete approved artifact matching current Oracle history is needed for a clean live preflight. Existing successful history/logs and recorded successful preflight remain available; the frontend and backend continue to fail closed on incompatible packages.

## API capabilities intentionally not invented

- PostgreSQL/MySQL/SQL Server engine discriminator, catalog/database metadata, drivers, discovery, TLS/version compatibility and execution support.
- Persisted connection-test timestamps or continuous database health. Registry Active is configuration; connections are tested explicitly.
- Aggregate fleet metrics, global collection totals and a live cross-instance schema catalog. The catalog browses registered schemas for one authorized instance using existing pagination.
- SSE/event streaming, execution percentages/ETA, running cancellation, rollback, generic retry, or approval workflow. Existing polling, queued cancellation and separately recorded repair evidence remain authoritative.
- Engine-specific historical snapshots for future engines. Existing destination/account/history snapshots remain intact.

These gaps do not require backend expansion for the implemented Oracle workflow. Clean successful live preflight depends on an artifact matching the lab's current Flyway history; no history repair or migration change is included in this review.
