# Run Details UX design review — 2026-10-17

The focused redesign uses the [existing Oracle Console Stitch project](https://stitch.withgoogle.com/projects/9773888998249082148). Existing Run Details screen `03a36bf557ae4f4c8c6528ceafe7df80`, the deployed application, retained run records, native Info rendering, immutable artifact APIs and existing console tokens were inspected before requesting these refinements.

| View | Stitch reference |
| --- | --- |
| Run Details Overview | `projects/9773888998249082148/screens/d4de1d373a1247bfa1c5d8a1d039b614` |
| SQL Scripts desktop | `projects/9773888998249082148/screens/e7837470cf774f5e8c4e469ec173f34a` |
| Migration Info desktop | `projects/9773888998249082148/screens/ef2ad4fa8fa24e7e8c2a355dcf173f39` |
| SQL reader mobile | `projects/9773888998249082148/screens/a6ddc24a219d4dd2a76d8c3837ee9ea2` |

Stitch also returned a supplementary Overview `35ade71c648e433da29d5281751e8a0b` with the mobile generation; it was not a request to regenerate other product screens. No new Stitch project was created.

The desktop proposals and mobile reader were visually reviewed. Accepted structure: the existing sidebar/header, Inter and JetBrains Mono, neutral canvas/white surfaces, teal navigation, compact status and destination header, Overview / Migration Info / SQL Scripts / Execution Logs, a script explorer with a separate read-only reader, native Info table with row inspection, and optional full-screen mobile reading. Original JSON and repair audit remain in disclosures. A successful run with Repair SKIPPED uses quiet text rather than a repair alert.

Stitch generated illustrative metadata despite the prompt: Oracle 19c/PROD/TCPS, fictional accounts/projects/artifacts and SQL, statement counts, timings, object impact, signed digests, WORM claims and transaction assertions. Those are **not evidence or product requirements** and are excluded from implementation. The real fixture is Oracle Free / FREEPDB1 / FWM_WZ260_A / DEV, Flyway 13.10.3. Displayed identities, state strings, timestamps and outcomes come from the existing authenticated APIs. The native Flyway checksum is never relabeled SHA-256; source file SHA-256 is a separate immutable artifact value.

Implementation adjustments improve density and correctness: full capture metadata is disclosed behind a compact provenance strip; all seven native columns retain sorting and pagination through the existing DataTable; relative paths identify scripts without asserting execution order from explorer order. “Latest recorded Info” is a separate explicit destination. Missing historical Info never substitutes a newer capture. SQL links require an unambiguous native filepath/filename match in the run's effective manifest. The file viewer shows actual stored bytes after hash verification, with original packages kept secondary.

Mobile uses contained tab/table/code scrolling and a Radix full-screen reader. Copy preserves decoded UTF-8 content including BOM/CRLF; download preserves original bytes and filename. The reader presents 200 lines at a time, previews up to 2 MiB, bounds search to the first 10,000 occurrences with an explicit limit label, and falls back to plain text for unusually long displayed blocks. SQL highlighting is cosmetic, never an SQL/Flyway interpretation or execution control.

[Before screenshots and baseline](../../evidence/run-details-ux-20261017/baseline.md) and [functional after captures](../../evidence/run-details-ux-20261017/after/) are the implementation comparison. Stitch illustrations alone do not establish acceptance.
