# Stitch delivery checkpoint

## Scope and authoritative sources
- **Goal remains ACTIVE, not complete. This turn made verified progress.** Design-only; no application code/backend/Flyway execution/deployment.
- Full task: `/home/longhn0710/.codex/attachments/d8c429fb-965f-4428-bd5d-07aa5092505a/pasted-text-1.txt` (read in full).
- Canonical product spec: `../../flyway-manager-product-spec.md` (read in full); global user engineering instructions apply; no local AGENTS.md found.
- All five requested handoff documents exist in this directory; review is IN PROGRESS.
- Project: [Flyway Manager](https://stitch.withgoogle.com/projects/12852677200871379262), ID `12852677200871379262`.
- Design system: `assets/12038184256015313756`; LIGHT1440,#087E8B,canvas#F7F8FA,surface#FFFFFF,border#DDE3E8,ink#172B34,muted#546773,Geist+JetBrainsMono,sidebar216/header56,4pxgrid.
- Twelve required primary screen keys generated in six bounded batches, all IDs directly retrieved, all initial PNGs/HTML inspected. **S01/S03/S04 reviewed. Other screens still need corrections.** Do not mark goal complete yet.

## Current canonical candidates
| Key | Current ID | Review / remaining correction |
|---|---|---|
| S01 | `8212d91a138c444bb97ed5b9745d88be` | REVIEWED: direct screen + native1440 image/HTML; no overflow; zero visible text below12px |
| S02 | `747c44cb45fb44a98caa448e38528cbe` | Final narrowfix: RUN-1043timestamp11px; page-sizeselect clipped; align RUN-1047 actor/API/artifact/date andRUN-1048 artifact/date with linkedfixtures |
| S03 | `e748a7f71c0c4bfb925753cebb6b721b` | REVIEWED: direct screen+native1440 image/HTML; semantic corrections verified; no overflow; all rendered text>=12px |
| S04 | `d2ea2697893e4bda865aa19cb743d858` | REVIEWED: direct screen+native1440 image/HTML; semantic corrections verified; no overflow; all rendered text>=12px |
| S05 | `2c700bbcbc82435ea67b3a3aa7f57075` | Final narrowfix: 4steppertexts10/11px, elapsed42s, validate16combinedfiles, plaintextsearchhint; toolbar fixed/nooverflow |
| S06 | `84663b2d072b4f92888426c6d6d143a1` | Final narrowfix: stackArtifactID+longfilename toavoidlabelcollision; removegrep/regexhint; failure/repairguards andfonts reviewed |
| S07 | `7eb10aa292864089b33e2c08e141c062` | Final one-cell regression: CORE_BANKING history location replaced by UAT; restore CORE_BANKING.flyway_schema_history. Native1440/fonts0, shell and hidden registration form reviewed |
| S08 | `1f32eadf60014e0d8c995a6fff31e8bd` | REVIEWED: native1440/HTML/image, fonts0/nooverflow; explicit history owner/table+alternative account; guarded repair policy; no container/path/TLS-version claim |
| S09 | `9cf3e291289c4c3a93fadd2f6e7f5c59` | Final tiny style: two breadcrumb slashes10px to12; moveSettingsbelowServiceAccounts; write-only credentials/mappings/source/native1440 reviewed |
| S10 | `795bdb669cab4220aa26a250cdb94aa1` | Final CSSfix: actualDOMsingle7nav but justify-between places nav atbottom(y738); usejustify-start. PROD header/scope rows fixed; GUID/filters/scopes/fonts0 reviewed. Uniqueviewport provescurrentstate. |
| S11 | `9c83b377e7f7454d95b42530b21038cb` | Pendingrefine: tokenhashnotAES; stableIDs/exact202URLs/per-runSSE/HTTPvsjobfailure; pertokenactions; lastusedRUN1047; fonts/shell |
| S12 | `fdb8929f85974125ad8513f39c7d9fba` | Pendingrefine: noFIPS/globalPROD/forced90dayexpiry/hashparams; SQLiteauth; directLDAPbindNOsavedLDAPpasswords; optionalproviders+PKCE/recovery; fonts |

## Latest evidence and canvas
- `evidence/screens.json` is authoritative current source metadata (URLs/IDs/review notes/prior revision IDs). `screen-inventory.md` mirrors it.
- Native1440 fullpage PNGs and ID/source-hash/font/width results: `evidence/viewport-audit.json`, `Sxx-1440.png`. Current S01–S08 all rendered pageWidth1440. Small rendered text counts: S01=0,S02=1,S03=0,S04=0,S05=4,S06=0,S07=26,S08=9. Visual images were actually inspected, not inferred from metadata.
- S01 cleanlogin, S03 restored artifact/hash, S04 correcthistory/count/authorization all verified. S02 filters/overlap fixed but timestamp/page-size select still need correction. S05/S06 toolbars now disjoint and full; remaining small issues below.
- **Canvas synchronized to all12 latest canonical candidates** through authenticated REST PATCH; HTTP200 and fresh REST/MCP GET confirm12 visible sources, all logical widths1440; six previously attached older revisions hidden/preserved. `evidence/canvas.json` has actual instances. No project/screen resource deleted.
- Stitch screenshots themselves are2560pxraster; downloaded at=w1440. Native CSS1440 evidence comes from isolated browser rendering of retrieved design HTML, not thumbnail resizing.
- All data/versions/dates are illustrative design fixtures, not real Oracle execution or supported-version certification. Linked fixture definitions in information-architecture.md. No interactive runtime/API/mobile/keyboard acceptance has been claimed.

## Next actionable work (no user confirmation needed)
1. Finish S07/S08 narrow follow-up: actual fonts below12 remain; remove duplicated sidebar brand and unverified TLS1.3 labels; S07 footer must not promise DDL integrity. S08 remove false runner-container and fake `fs:/var/flyway-runner/...` path, show only server-managed immutable historical+new set/ephemeral local filesystem; no Docker dependency. Ensure EXPLICIT history-owner input/default/dedicated state plus history-table input and authorized alternative-account control(APP_MIGRATOR), while preserving guarded Admin auto-repair policy and current correct semantics. Inspect hidden S07 registration drawer by rendering that state too.
2. Refine S09/S10: see table. For S10 DN/email are mutable; show provider + objectGUID/entryUUID; roleAdmin doesn't automatically grant PROD, Operator may receive explicitPROD permission; remove invented bulk LDAP/OIDC sync operation (link provider settings instead). Inspect lower scrolling PROD scope editor, separate SQL download.
3. Refine S11/S12: tokenhashed-at-rest (notOracleAES), prefix-only list, once-only issue, scope/expiry/rotate/revoke; API exact snake_case stable resource IDs and202 status/logURLs, SSE `/api/v1/migration-runs/{id}/events`, GET can200 withFAILED and client exitsnonzero. S12 LocalSQLite/Argon2id, LDAPdirectbind with NO persisted LDAPpassword (not even saved service-bind password); transient tests; OIDCcode+PKCE issuer+sub; noFIPS/version/forced90daypolicy.
4. Final narrow S02 fix: last RUN1043 timestamp11px→12; page-size selects>=36px,lineheight20,padding/no clipped text. Align RUN1048 artifactfinance_v3.zip/start2025-05-19 14:32:10; RUN1047 sourceAPI/actorci-finance-uat/artifactfinance_v3.2.0_patch_idx.zip/start2025-05-18 22:15:02/duration36s with S06.
5. Final narrow S05/S06 fix: S05 four steppertexts10/11→12, elapsed00:00:42 (currently1m42), validate16combinedfiles=2history+14bundle, no grep/regexhint. S06 stack ArtifactID+longfilename to avoid labelcollision; terminal logfollow label must clearly mean scroll, not resumeexecution ('Log đã kết thúc — FAILED'). Preserve complete conditional-repair safety checklist and separateFAILED/repairSUCCEEDED/NEEDS_REVIEW.
6. After each≤2screen batch: directgetscreen, downloadHTML/PNG, actualimage+HTML inspection, native1440QA, update this checkpoint/currentregistry/inventory. Literal narrow text/style edits work better than broad requests; generator summaries repeatedly overclaim. If edit returns503/timeout, inspect current state before any new attempt; do not blindly repeat.
7. Final reconcile12 canonical canvas instances, hide/preserve only known superseded revisions; requirement-by-requirement audit against full task/spec, update concise design-review and current progress, only then `update_goal(status="complete")`. Original objective must stay intact.

## Tooling and recovery
- Temporary QA helper `/tmp/flyway-stitch/qa.cjs`: `node /tmp/flyway-stitch/qa.cjs S07 S08`. Uses cached isolatedChromium and `/home/longhn0710/workspace/kelryvo/node_modules/playwright-core`. Source inspection cache `/tmp/flyway-stitch/Sxx.html`; no generated application source is placed in repo.
- For revised source: map Sxx from returned title (results can reverse order), retrieve via `get_screen`, keep priorIds; archive previous PNG into `evidence/revisions/`; update `evidence/screens.json`. Then run helper and inspect actual images. Browser process sessions must be polled via write_stdin, not restarted merely for observation timeout.
- MCP generation/edit returns new screen IDs; list_screens/project may omit unattached generated sources. Authenticated REST GET/PATCH works: `https://stitch.googleapis.com/v1/projects/12852677200871379262?updateMask=screenInstances`. Read current instances, merge canonical sources, preserve unknown/current design-system asset, hide known superseded sources; do not delete.
- Credentials: protected local helper `/home/longhn0710/.codex/bin/stitch-mcp-headers.py` yieldsJSONheaders. Capture internally, NEVER print key or headers. Raw tools/list/get confirmed no native canvas-update MCPtool. REST canvas sync is authorized design-content work, not infrastructure/deployment.
- Native canvas layout:3columns,x0/1536/3072,width1440; firstrowy700 belowdesignsystemasset; rowheight=maxnativePNGheight+128. Persistactualcanvas verification. No deployment,push,merge or external messages authorized.
