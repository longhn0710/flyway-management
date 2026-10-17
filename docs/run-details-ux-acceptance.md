# Run Details, immutable SQL and native Info UX acceptance — 2026-10-17

Implemented locally on `feat/run-details-ux` in the separate `flyway-management-run-ux` worktree, starting at clean master `c93f158`. The untouched-source `make check` passed. The deployed application was inspected read-only and loaded the same `index-BsOYbtNb.js` bundle produced by that baseline; the existing release evidence identifies `c93f158`. The original `feat/postgresql-foundation` checkout, its source/evidence and unrelated working-tree changes were preserved.

Run Details now provides Overview / Migration Info / SQL Scripts / Execution Logs. The header presents the recorded run outcome and destination first. Overview shows requester, available execution-account identity, current project membership (explicitly current), artifact identity, pinned runtime, timestamps/duration and separately recorded preflight/migration/validation outcomes. Missing execution evidence is labeled “Not recorded.” Repair SKIPPED on success is quiet metadata, with audit evidence retained under Advanced Diagnostics.

Migration Info reuses the existing native capture API and shared native rendering. It has all seven required columns, sorting (including Installed On), state/category filters, search, pagination and complete row details. State strings remain native, including unfamiliar states. Capture timestamp, source artifact, runtime and historical scope are visible; full metadata, effective manifest and original redacted JSON/stdout/stderr are available in disclosures. Requests include the exact selected `run_id`; no missing capture is replaced by a newer capture. “Latest recorded Info” is an explicit separate navigation action. Existing standalone refresh behavior is retained.

SQL Scripts lists the effective files reserved for this historical run, with their immutable artifact provenance. Each file opens individually with its original relative path, filename, kind/version, size, SHA-256 and a filename-derived description clearly labeled as such. Native Info row details link to SQL only when the native path/filename matches an unambiguous effective file. The viewer has cosmetic syntax highlighting, line numbers, occurrence search/navigation, Copy SQL, original download and mobile full-screen reading. Explorer path order is not presented as Flyway execution order. Original submitted packages are secondary and can contain additional files outside this run's effective set.

Two read-only endpoints were necessary because the prior run-artifacts API returned entire contributing bundles rather than effective run membership:

- `GET /migration-runs/{id}/files` returns `(artifact_id, path, sha256, kind, version, size)` for the reserved effective set.
- `GET /migration-runs/{id}/files/content?artifact_id=…&path=…` reuses existing immutable SQL byte retrieval after checking run permission, exact run membership, artifact permission, manifest identity, configured size limits and content integrity.

No generic file browsing, filesystem extraction or SQL execution endpoint was added. Original SQL/package download APIs remain compatible. Missing reserved content fails closed. Empty reservation sets remain explicit. Existing SQL/ZIP/TAR/TGZ normalization, path/symlink/unsupported-entry rejection and upload limits remain unchanged; no new package formats were added. SQL is always rendered as React text, never HTML.

| Verification | Result |
| --- | --- |
| Untouched master baseline `make check` | PASS |
| `make check` (build, vet, backend tests, lint, frontend unit tests) | PASS; 22 frontend unit tests |
| `make test-e2e` | PASS; 41 browser tests, including nine focused UX edge tests |
| `make test-race` | PASS |
| `make security` | PASS; npm audit zero vulnerabilities; govulncheck zero called/imported-package vulnerabilities |
| OpenAPI 3.1 validation | PASS using existing validator environment |
| Retained Oracle run through real local Manager APIs/browser | PASS; two native rows and exact V1/V2 SQL, clipboard and downloads |
| Artifact HTTP/core safety tests | PASS; scoped Viewer access, forbidden scope, cross-run membership, traversal, corrupted bytes/hash, missing content, empty membership, configured size limit and cursor release |
| Real archive upload → normalized effective manifest → original SQL/package retrieval | PASS using the deterministic HTTP runtime fixture; no Oracle connection |
| Private credential/key-marker and gitleaks review | PASS; no private credential matches or leaks |

The retained fixture is `DG5lFUs-8nPKq7_Ra8hqNB`: Flyway 13.10.3, Oracle Free / FREEPDB1, FWM_WZ260_A, V1 and V2 Success, Repair SKIPPED. It is a private SQLite online-backup copy with its original external key copied separately. The local Manager has no configured Flyway executable, so acceptance reads existing records without contacting Oracle. The browser permitted only login and GET/HEAD. Both Info rows rendered without expanding JSON; each opened its correct stored SQL. Preview, clipboard and downloaded files matched the retained content; checksums, search, line numbers, highlighting, full-screen reading, desktop/mobile layouts, direct links/reload and log search/channel/follow were exercised. Original backup/key hash preservation is recorded in the retained browser result.

The standard browser suite also exercises the existing Oracle-shaped workflow with a controlled fake runtime: real upload, preflight, successful migration, skipped repair, downloads, native Info and logs; a failed case still remains failed after the existing narrow repair. These are regression fixtures, not new Oracle migration acceptance. Focused UI response fixtures cover long/nested/repeatable scripts, unfamiliar native states, missing/empty Info, pagination/sorting, missing/corrupt/unauthorized artifacts, untrusted HTML-like SQL, keyboard focus return and repetitive search. Existing artifact tests cover supported archive formats, unsafe paths, symlinks, nested archives, unsupported entries and expansion limits.

The SQL preview limit is 2 MiB with streaming enforcement. Larger files remain available through the authorized original download. Preview requires UTF-8 text without NUL bytes; incompatible encoding/binary content has an explicit download state. Copy preserves decoded UTF-8 BOM/CRLF, while visible lines normalize CRLF for display. The viewer pages 200 lines, indexes at most the first 10,000 search occurrences with an explicit limit message, and uses plain text for exceptionally large displayed blocks. Highlighting is cosmetic, not a database parser. These limits do not change stored bytes, checksums, artifact identity or runtime behavior.

Vite retains the baseline non-failing chunk-size advisory. Govulncheck retains a required-module advisory outside called/imported packages. No dependencies or pinned runtime versions changed. Standard browser suites regenerated historical fixture screenshots/catalog outputs; those outputs were isolated outside this implementation package and the tracked historical screenshots restored.

[Stitch references and design review](design/run-details-ux/review.md) · [Deployed before screenshots](evidence/run-details-ux-20261017/baseline.md) · [Retained browser after evidence](evidence/run-details-ux-20261017/after.md) · [Retained result](evidence/run-details-ux-20261017/after/retained-fixture.json) · [Desktop native Info](evidence/run-details-ux-20261017/after/migration-info-1440.png) · [Mobile SQL](evidence/run-details-ux-20261017/after/sql-reader-390.png) · [Mobile full-screen reader](evidence/run-details-ux-20261017/after/sql-fullscreen-390.png)

Final check transcripts are retained in `evidence/run-details-ux-20261017/checks/`. The reproducible retained browser runner is `scripts/acceptance/run-details-retained.mjs`; it requires private copied fixture/key and login inputs via environment variables, with no credentials in Git. Portable unit/API/browser fixtures run through the existing Make targets.

No Flyway CLI command, migration resolution/state definition, history, ordering, transaction behavior, repair eligibility, checksum policy, artifact identity, retry/recovery policy or database engine implementation changed. At completion of the local acceptance above, no new Oracle migration or connection, push, merge, publication, deployment, registry upgrade or infrastructure change had been performed. The user subsequently authorized publishing a focused PR and merging into master after successful CI. Deployment, registry upgrades and infrastructure changes remain outside this task.
