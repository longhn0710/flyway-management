# Design review — evidence ledger

Status: **IN PROGRESS — NOT READY FOR IMPLEMENTATION HANDOFF**.

Project: https://stitch.withgoogle.com/projects/12852677200871379262

## Evidence standard

A screen is complete only after direct Stitch `get_screen` confirms existence, screenshot is actually inspected and available HTML is reviewed against the spec. Generator prose is not proof. Screenshots are stored in `evidence/`; metadata/IDs in `evidence/screens.json`. Generated HTML is inspection material under `/tmp/flyway-stitch/`, not application implementation.

## Batch 1 findings

| Screen | Inspected evidence | Finding / status |
|---|---|---|
| S01 original `a450fc19c6a747b6bab8c2254b6620b1` | PNG + 20,111-byte HTML | LIGHT/teal, Local/LDAP/SSO exist. Remove unrelated PORT1521, database names before auth, false enterprise version/compliance, remember-me and incorrect local encrypted-password wording. **Needs refinement**. |
| S02 original `e3c44e3042ab44bfb722268c2cf45b42` | PNG + 31,562-byte HTML | FAILED + separate repair SUCCEEDED correct; page min-width clips primary action/table/footer. Requester filter absent. **Superseded**. |
| S02 revision `ff73349ee99c49909030a02943915a38` | PNG + 31,106-byte HTML | Main min-width fixed; CTA, requester filter and pagination visible; bounded table scroll. Source WEB/API filter was removed during refinement. **Needs narrow filter correction**. |

Multi-screen edit claimed to refine S01 and S02 but returned only S02. Direct get_screen confirms S01 source unchanged. Subsequent individual S01 edit returned HTTP503; completion unknown until re-observed. Do not repeat blindly.

## Current verification limits

Refinement batch 4: S07 `1952458c390f4fa2a6f7c9212024791c`, S08 `2e49db1ffb2f44019e655f773e776f44` retrieved, native1440 images/HTML inspected. S07 unobscured registry now visible; registration drawer remains hidden in HTML; new-target auth/privilege probes correctly not-tested without account and enabled does not imply runnable. S08 corrected schema/login distinction, managed historical+new set, CURRENT_SCHEMA POC wording, guarded Admin auto-repair policy and linked run actors. No page overflow. **Pending:** S07 has26 rendered textnodesunder12px; S08 has9. Both retain unverified TLS1.3 labels and duplicated sidebar branding. S08 introduced an incorrect 'runner container' claim and fake `fs:/var/flyway-runner/...` path; remove them (native standalone process, server-managed ephemeral filesystem, no container requirement). Restore explicit history-owner control and authorized alternative-account selector if absent; do not silently drop these controls. S07 footer must not promise Oracle DDL integrity/rollback. None of S07/S08 is marked final.

Refinement batch 3: S05 `2c700bbcbc82435ea67b3a3aa7f57075`, S06 `84663b2d072b4f92888426c6d6d143a1` retrieved, native1440 PNGs/HTML inspected. Both log toolbars now have distinct search/filter and follow/download rows; no page overflow. S06 correctly preserves FAILED + separate repairSUCCEEDED + NEEDS_REVIEW, complete safety checklist and trusted actor/untrusted context; zero textbelow12px. Remaining narrow fixes: S06 long artifact metadata collides with its label (stack ID and filename); S05 has4 small stepper texts10/11px, elapsed still1m42 rather than fixture42s, and validation log should count16combined files (2history+14bundle). Plain text log search suffices; remove unsupported grep/regex hint.

Refinement batch 2 follow-up VERIFIED: S03 `e748a7f71c0c4bfb925753cebb6b721b`, S04 `d2ea2697893e4bda865aa19cb743d858`. Direct retrieval, native1440 images and HTML inspected. Artifact ID/full illustrative SHA restored, no processor-label regression; broad SQL syntax claims removed; application actor authorization explicit; correct stream serialization,16combined files and separate history fields retained. Both measured pageWidth1440 and zero rendered textbelow12px. S03/S04 reviewed. No execution or repair POC implied.

Refinement batch 2: S03 `87b08a1135bc42d293e4fc6b65bef238` and S04 `d825bb85b15b4619b2dbc180bf0185a3` directly retrieved, native1440 PNGs inspected, HTML reviewed. Oracle lock claims removed; application stream serialization now correct. S04 distinguishes14new+2historical=16 and separate history owner/table. No page overflow; S03 has zero textunder12px; S04 retains a10px step numeral. **New regressions found:** S03 bundle artifact/checksum text was replaced by 'Artifact Stream Processor' despite correct summary ID; restore actual illustrative values. Remove remaining broad 'SQL syntax checked' wording. S04 authorization card refers to Oracle DEPLOY_ADMIN rather than authenticated application actor; separate app authorization from Oracle grants, and remove subtitle implying complete Oracle syntax validation. These are not marked complete.

Refinement batch 1 actual evidence: S01 `8212d91a138c444bb97ed5b9745d88be` directly retrieved and inspected at native1440; unsupported claims removed, Local/LDAP/OIDC retained, no whole-page overflow, computed visible text under12px **zero**. S01 visually/content reviewed. S02 `747c44cb45fb44a98caa448e38528cbe` has all required filters including WEB/API, bounded table and separated note/pagination rows; actual1440 view confirms overlap fixed. Pending narrow style fix: last-row timestamp remains11px and page-size select clips its displayed text. Audit `evidence/viewport-audit.json` is tied to actual source hashes and screen IDs; it contradicts the generator's blanket claim that all S02 text is12px.

S12 requires a further semantic correction: canonical spec prohibits persisting LDAP passwords; provider settings must use direct LDAP bind and transient test credentials, not a saved service-bind password. Interaction contract corrected to preserve the full requirement.

Batch 6: S11 `9c83b377e7f7454d95b42530b21038cb` and S12 `fdb8929f85974125ad8513f39c7d9fba` retrieved; actual PNGs inspected and HTML fetched. S11 includes one-time token state and explicit PROD scope, but token footer wrongly implies AES encryption instead of hash-at-rest, SSE example uses `/api/v1/events` instead of per-run route, and response/payload/actions need exact API audit. Remove bulk-revoke emphasis and ensure per-token rotate/revoke visible. S12 includes all three provider forms, immutable LDAP objectGUID and OIDC issuer+sub/PKCE; remove invented FIPS certification/global PROD environment, mandatory 90-day rotation/unsupported hash parameters and Oracle-schema wording for local user storage. Preserve write-only replacement and last-usable-admin recovery. No screen is final until corrections and native viewport review pass.

Batch 5: S09 `ca779e4cd9cc4818a7ffbf6b6dccc1c4` and S10 `1c7e14e8c15441858dfe304497f96326` directly retrieved and PNGs inspected. S09 shows write-only replacement, no saved-secret eye/copy, local default and optional Vault, multiple schema defaults and separate connection probes. Remove false claim that passwords never appear in transient memory and invented version/global-target header labels. S10 has separate SQL-download and per-schema grants, but LDAP DN is incorrectly described as immutable subject; replace with objectGUID/entryUUID and keep DN only as mutable display attribute. Inspect actual PROD grant control in scrolling editor and make it explicit if missing; clean invented version/global Oracle node labels and do not imply Admin automatically executes PROD. Shared navigation footer positioning also needs consistency.

Batch 4: S07 `493613690acb41109f63a067211df4da` and S08 `3a96a2084dd9453a82aff3de914dfa8a` retrieved; actual PNGs inspected and HTML available. S07 includes registry/schema inventory plus open registration drawer; native base-state render with drawer closed is still needed to inspect the obscured table. S08 correctly shows separate history locations, shared account, guardrails and role/Oracle privilege warning. Corrections required: replace Java `classpath:` location with server-managed immutable migration set (native CLI uses reconstructed filesystem); target schema label must not imply Oracle login user; dedicated-history note must require POC verification of CURRENT_SCHEMA/unqualified SQL behavior, not invent a CURRENT_SCHEMA privilege or ban unqualified SQL.

All displayed target names, dates, versions, health states and repair evidence are illustrative design data, not evidence of actual Oracle execution, supported Flyway pin or safety acceptance.

Native viewport check now available using cached isolated Chromium/Playwright against retrieved Stitch HTML (not application implementation): S01–S04 rendered at `innerWidth=1440`; `documentElement.scrollWidth=1440` for all four. Native evidence `S01-1440.png`–`S04-1440.png`. This closes native desktop width/whole-page overflow uncertainty for these revisions only; it does not verify mobile, keyboard or all table cells being visible without local scroll.

Batch 3: S05 `b3fd22c0aa14460096c4b64d0f979e51` and S06 `a15b86908cb1442f8046f3a6e1da063d` directly retrieved; actual PNGs inspected, HTML downloaded. Running and failed/repair outcomes represented correctly; S06 separates API actor from untrusted caller context and warns about partial DDL. Pending fixes: S05 log toolbar overlaps search/source/follow controls; S06 needs visible search/level/source/download controls and a readable unwrapped Run ID. Remove invented node/version/policy footer labels. Keep shared metadata/version examples consistent and document them as illustrative, not supported-version claims.

Canvas discrepancy reproduced through a fresh authenticated MCP HTTP connection, so it is not just the current client cache. Relevant upstream reports: [canvas synchronization capability missing](https://github.com/google-labs-code/stitch-sdk/issues/365) and [generated screen indexing delayed](https://github.com/google-labs-code/stitch-sdk/issues/150). These reports support a possible upstream limitation; our direct observations remain the evidence for this project. No MCP tool to attach/update canvas instances was exposed by a fresh tools/list request.

Workaround verified: authorized authenticated REST PATCH of the `screenInstances` field, merging all six known canonical screen references with existing project content. HTTP200 and fresh GET confirm six sources plus the design-system instance. `evidence/canvas.json` records actual source/instance references. Canvas attachment is now resolved for S01–S06; repeat additive sync after remaining canonical screens/refinements.

Batch 2: S03 `e3c0d04855e2424ba13886b5b2c5db76` and S04 `97fc3e795af7429c8d313b3afd094f50` directly retrieved; PNGs inspected and HTML downloaded. Separate target/schema/account controls, upload variants, artifact summary and fixed guardrail flags exist. **Refinement required**: generated Oracle Lock cards falsely describe a persistent Oracle history-table/row lock; replace with application stream serialization. Remove implied complete SQL syntax/parser validation; only supported capability checks. Correct qualified `flyway.table` wording to separate history owner/table, and distinguish bundle file count from historical+new combined locations. Shared header wording must be unified. No completion claim for these screens yet.

- Stitch metadata reports 2560px raster width despite requested 1440px desktop. Downloaded screenshots at `=w1440` are 1440×1152 resized raster; this is **not proof of a native 1440 CSS-pixel viewport**. Native 1440 layout verification remains open; generation has no viewport-width input.
- `list_screens` has returned an incomplete list while direct get_screen succeeds for newer IDs. Direct screen retrieval is authoritative for existence; project canvas references need final reconciliation.
- No browser keyboard, focus trap, true responsive behavior, contrast-by-component or API/RBAC execution has been tested. These remain implementation checks; visual inspection must not be described as interactive testing.
- Repair safety POC, exact Flyway/JRE/JDBC edition/version, distribution licensing and native packaging gates remain open per canonical spec. Design does not claim these implemented or proven.

## Completion audit (pending)

S01–S12, shared visual system, explicit migration/repair distinction, required administration, native 1440 layout evidence, final project/ID reconciliation, all handoff documents and unresolved-gap disclosure must be checked before marking the goal complete. No application code, backend, Flyway execution or deployment has been created for this design task.


S07/S08 follow-up retrieved and inspected at native1440: both no page overflow and zero rendered textbelow12px. S08 semantic/config controls verified. S07 hidden drawer rendered and inspected (S07-drawer-1440.png), truthful not-tested auth/probes preserved. New single-cell regression: CORE_BANKING history location became UAT; fix that cell only before final. No broad redraw needed.


S09 new9cf3e291289c4c3a93fadd2f6e7f5c59 retrieved and visually/source inspected at1440. Correct write-only empty password inputs, mappings, encrypted local default/optionalVault, no false memory guarantee/globalversion; no pageoverflow. Only two breadcrumb separators remain10px. S10 generator prose claimed edits but returned no S10record; direct get_screen and fetched HTML hash prove original source unchanged (objectGUID absent, bulk sync retained). S10 is not complete and must be edited individually.


S10 revised8d3d12026ee1433cbb5c351c42fedbd8 directly retrieved and native1440 image/HTML inspected. Correct immutable LDAP objectGUID, no bulk sync, explicit enabled unchecked PROD controls and role/scope distinction verified from source. Remaining11 small labels/tableheads; toolbar query and LDAP/Operator filters conflict with four visible mixed-provider rows, so reset toolbar toAll without changing selected editor. Inspect lower internal-scroll PROD section after correction.


S10 controlled single Refine variant3f256edb661947589d74c5934bb06f1e retrieved; actual native1440 images inspected, zero small text/no pageoverflow. Toolbar query/Allfilters coherent with4rows. Lower PROD state rendered and inspected; evidenceS10-scope-state.json proves execute/upload unchecked and enabled, readlogs checked, SQLdownload separately unchecked. Correct GUID and no bulk sync preserved. Earlier prose-only edit was a no-op by direct source hash; this actual variant supersedes it.


Image inspection overturns the premature S10 reviewed label: variant3f256edb661947589d74c5934bb06f1e duplicated sidebar navigation (top6 plus bottom7 links), and PROD heading badges collide. Must remove duplicate nav and wrap PROD heading/scope-resolution rows. Semantic controls and font/width checks remain valid but do not prove layout completion.


S10 latest795bdb669cab4220aa26a250cdb94aa1: fresh viewport screenshot S10-795-top.png and DOM inspection establish ONE actual seven-link nav at y738; full-page/reused-path images were misleading about duplication. Remaining actual issue is aside justify-between putting nav at bottom. Replace with justify-start/top flow. PROD header now two disjoint rows and ScopeResolution separate rows. Use revision-ID filenames for future native screenshots to avoid reused-path ambiguity; do not mark layout passed from nav-count alone.
