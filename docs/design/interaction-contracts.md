# Interaction contracts

Applies to S01–S12. Canonical resource semantics: [spec](../../flyway-manager-product-spec.md), especially §§3–7. These are design contracts, not implemented endpoints or verified runtime behavior.

## Navigation and session

- Login offers explicit Local and LDAP/AD provider selection and a separate OIDC SSO action. No silent local fallback for an LDAP username. Bind external identities by provider/issuer + immutable subject, never email alone. Generic invalid-credentials text; preserve username/provider but clear password after failure. Rate-limit/locked/unavailable states are accessible inline messages without account enumeration.
- After authentication, open `/runs` or a validated same-origin deep link. Sign out clears session and sensitive drafts. Session expiry preserves non-secret draft identifiers, then rechecks authorization after login; never automatically submits a migration.
- First launch: protected one-time bootstrap dialog establishes initial Admin and native data directory/key setup. It is unavailable after initialization; no public registration. Display recovery/backup guidance without exposing key bytes. This is a dialog state, not a thirteenth primary screen.
- Shared sidebar order and routes are in `information-architecture.md`. Admin-only management controls are hidden from other roles; direct unauthorized URLs still return backend authorization errors. Back to History restores URL-based filters/page. Unsaved settings prompt before leaving; migration draft Back preserves chosen artifact/target, then revalidates stale state.

## RBAC behavior

| Role / identity | View scoped data | Upload/submit | Registry, credentials, users, providers |
|---|---|---|---|
| Admin | Yes | Granted stream; PROD separately explicit | Yes |
| Operator | Yes | Granted target + schema only | No |
| Viewer | Yes | No; hide primary execution actions | No |
| Service account | Token-granted actions/resources | Explicit token action + target/schema + PROD grant | Only explicitly authorized API administration, if supported |

Every backend action re-evaluates `(actor, action, target_id, schema_id)`. Disabling a UI control is not authorization. SQL-content download can be denied while run metadata/logs remain readable. Never grant PROD implicitly through a global role. Empty grant sets grant nothing. External newcomers have no privileged default role.

## S03/S04: bundle, validation and confirmation

1. Select one registered Oracle service/PDB target → one authorized target schema → a mapped execution account, default preselected. Changing target clears incompatible schema/account; changing schema re-evaluates account and history. A reusable account can map to multiple streams; it does not merge their version sequences.
2. Provide distinct controls for SQL file(s), folder upload and archive. Accept single/multiple `.sql`, folder relative paths, `.zip`, `.tar`, `.tar.gz`, `.tgz`. Optional compatibility formats are unavailable until tested support is enabled. User supplies neither manifest nor Flyway config.
3. Upload progress is cancellable before artifact finalization. Server detects structure/signatures and checks limits; do not trust extension. Reject traversal/absolute paths, links, unsupported/nested archives, malformed input, excessive count/size/expansion. Proposed defaults (50MB upload, 200MB extracted, 1,000 SQL, 20MB/file) must be labelled configurable/proposed until accepted. Error identifies safe relative filename and reason, without exposing server paths.
4. Inspect immutable artifact ID, original name/type/size/SHA-256, extracted totals, normalized path manifest and each SQL checksum. Show Flyway-discovered version/repeatable ordering, prior applied state and combined historical migration set. Preserve original bytes and filenames. Invalid naming, unsupported Oracle/SQL*Plus feature, missing historical version and checksum drift are blocking findings; never silently rename, reversion or weaken validation.
5. Review summarizes environment, service/PDB, target schema, execution account NAME, history location, frozen configuration, artifact ID/checksum and ordered migration changes. No password. Fixed guardrails: validateOnMigrate=true, outOfOrder=false, cleanDisabled=true, baselineOnMigrate=false, createSchemas=false. Pinning/options still require Oracle POC; do not advertise an unverified version as supported.
6. Primary Submit requires validation READY, authorized scope, explicit confirmation and current snapshot. PROD has an additional explicit environment acknowledgment when permission exists; this is confirmation, not an approval workflow. Stale config/artifact/scope revalidates and blocks submission until reviewed again.
7. Submit asynchronously with a stable idempotency key. Disable double-click; preserve key/payload across transport retry. `202` opens the returned Run ID; identical request returns the same ID; conflicting key reuse (`409`) is actionable and never starts another SQL execution. Ambiguous transport timeout first checks request/run status; do not create a fresh execution key blindly.

## S05/S06: lifecycle, logs, failure and repair

| Phase | UI behavior | Allowed next behavior |
|---|---|---|
| UPLOADING / VALIDATING | File progress, normalization/check findings | Stop draft upload or fix input |
| READY | Reviewed frozen summary | Confirm async submission |
| QUEUED / PREFLIGHT | Queue position/reason and preflight progress | Cancel only before execution, if backend confirms cancellable |
| RUNNING | Visible target/schema/account/environment, elapsed time, live logs | Observe/download; no SQL retry/rollback/manual repair |
| REPAIRING | Migration outcome already FAILED; separate repair activity | Keep stream serialization through completion |
| SUCCEEDED | Migration success, exit 0 and completed time | View evidence |
| FAILED | Immutable migrate error/exit code remains visible | Inspect separate repair outcome and safety evidence |
| BLOCKED / NEEDS_REVIEW | Reason, uncertain/unsafe stream notice and evidence | Contact responsible DBA using policy guidance; no blind action |
| CANCELLED | Pre-execution cancellation recorded | New intentional run is a separate workflow |

- The active sequence serializes **per database target + schema** through migrate and any conditional repair. Label it as application stream occupancy, not a permanent Oracle table lock. Other streams may run independently but cross-schema DDL can still interfere.
- Run summary includes actor identity/type/provider, source WEB/API, untrusted requested-by/external context clearly labelled, target/service/schema/account, Flyway version from immutable snapshot, artifact/checksum, start/end/duration and exit code. Caller `initiated_by` does not replace authenticated identity.
- Log rows have monotonic sequence, time, level and stdout/stderr/structured-event source. Search + level/source filters, pagination, download and follow toggle. User scroll pauses follow; Resume returns to tail. Reconnect resumes sequence without duplicate rows. Optional SSE failure falls back to polling; disconnected is not failed execution. No secret exposure in logs or downloads.
- Persist migrate error, SQL snapshot, checksums, timestamps and history evidence **before** repair. Repair panel shows eligibility checks, configuration/location match, before/after history and independent operation ID/status/times. It must prove no unrelated checksum realignment or missing-version deletion; unproven safety skips repair.
- A failed-history row can be removed by safe repair; the migration still **FAILED**, CI/API still fail, no SQL is rerun. Partial Oracle DDL may remain committed. Successful repair never claims rollback or that schema is safe. Unsafe/failed/ambiguous repair retains error/evidence and BLOCKED/NEEDS_REVIEW stream guidance. Crash reconciliation precedes any new dispatch; no automatic re-execution.
- No manual Repair, Retry SQL, Rollback or Drop Object action. A corrected bundle is submitted deliberately through Run Migration after operational review and current safety/authorization checks.

## S07/S08/S09: registry, stream and credential forms

- Target form: stable ID/label, environment, host/port + Oracle service/PDB or supported descriptor, transport/TLS settings and enabled state. Service/PDB is required identity; hostname alone is insufficient. Connection result separates reachability, auth and probed privileges, with timestamp and sanitized errors. A successful probe cannot prove absence of cross-schema powers.
- Register schemas without creating Oracle schemas. Each has independent stream configuration, current version, history owner/table and account mappings. Default history is `<target_schema>.flyway_schema_history`; dedicated history owner/table requires explicit settings and POC validation of CURRENT_SCHEMA/unqualified SQL behavior. Reject duplicate physical history location within a target; name conflicting stream.
- Fix `schemas`, `defaultSchema`, table and migration locations per snapshot. Show configuration security note: Oracle grants govern access; Flyway schema settings are not a privilege sandbox. Broadly privileged execution credentials get a visible warning.
- S08 design choice: Admin may set stream repair policy to Disabled or Conditional. Conditional never bypasses eligibility/safety checks, does not certify the open repair POC gate, and does not expose a manual repair action. Ordinary roles see effective policy only.
- Execution account form is a separate resource: target(s), account name, provider (encrypted local default / external Vault reference), write-only password or secret reference, explicit allowed-schema associations, default/alternative selection. One account can map to CORE_BANKING and PAYMENT without shared history.
- Saved Oracle secret: metadata/masked placeholder only, **no reveal or copy**. Rotate by replacement; do not prefill old secret. For a new entered password a temporary reveal control is allowed. Do not send password back from API, embed in JDBC URL, logs or argv. Local master key stays outside SQLite; backup DB and key separately. Vault is optional, not required infrastructure.
- Save/test/rotate/disable actions have loading, success and sanitized failure states; permission or stale-update conflicts retain safe form values without exposing secrets. Confirm disable/rotation consequences on queued frozen snapshots through backend policy before mutation; active runs keep immutable recorded context.

## S10/S11/S12: access and authentication settings

- User list shows Local/LDAP/OIDC identity and immutable provider/subject mapping; role and target/schema grants edit together. PROD execute is an explicit separate grant. Scope tree has searchable target/schema rows and clear inherited versus explicit grant display. Confirmation identifies removed access; record administrative audit.
- Service accounts show owner/purpose, enabled state, token prefix/last-used/expiry and granted actions/streams. Issue token in a focused dialog with scope and expiry required. Reveal full token **once after creation**, with copy and 'Đã lưu token' acknowledgment. Closing loses readback; table only shows prefix, never full token. Rotation creates a new token, defines old-token expiry/revocation explicitly; destructive revoke confirmation names account/token and warns CI callers will fail. Never reuse Oracle passwords as service tokens.
- API examples use placeholders, `/api/v1` resources, `Idempotency-Key`, `202` run URLs and polling/SSE. Demonstrate FAILED + repair SUCCEEDED still returns pipeline failure. No vendor-specific integration plugin.
- Authentication settings are Admin-only tabs: Local password policy/recovery; LDAP/AD endpoint with required LDAPS or StartTLS and direct user bind, bind/test credentials entered transiently, **no LDAP passwords persisted** (including no saved service-bind password in provider settings); OIDC issuer/client/redirect URI, authorization code + PKCE, secret replacement and provider-subject binding. Providers independently enabled; no silent fallback. Validate issuer/URLs/certificates; sanitized test errors. Preserve an authorized recovery path and warn before disabling the last usable admin login method.

## API action mapping

All paths below have `/api/v1` prefix. Baseline routes are fixed by spec; administration subroutes and auth session method remain implementation contract decisions, not invented final API names.

| UI action | Resource / response contract |
|---|---|
| Load target/schema selectors | `GET /database-targets`; `GET /database-targets/{id}/schemas` |
| Upload/normalize | `POST /artifacts`; inspect/poll `GET /artifacts/{id}` |
| Submit reviewed run | `POST /migration-runs` with target_id, schema_id, execution_account_id, artifact_id and optional context; Idempotency-Key; 202 |
| Filter history | `GET /migration-runs` with approved query contract and pagination |
| Read status/repair evidence | `GET /migration-runs/{id}`; repair outcome independent |
| Logs/search/download | `GET /migration-runs/{id}/logs`; optional `GET .../{id}/events` SSE |
| Manifest/authorized SQL download | `GET /migration-runs/{id}/artifacts`; separate content authorization |
| Manage credentials | Admin resource family `/execution-accounts`; write-only secret fields |
| Users/service tokens | Admin resource family `/access/*`; token issuance value returned once |
| Providers/policies | Admin resource family `/settings/*`; never return saved secret material |

A successful status read can return HTTP200 with `status=FAILED`; the CI client maps terminal run outcome to its own nonzero exit code. Async submission remains HTTP202 when accepted. Do not confuse HTTP transport success with migration success. Example submissions use stable resource IDs returned by selectors/API, not target/schema/account display labels. SSE is scoped to `/migration-runs/{id}/events`, never a global `/api/v1/events` shortcut.

## Shared states and dialogs

| State | Contract |
|---|---|
| LOADING | Layout-matched skeleton, labelled busy region, disable only conflicting actions |
| EMPTY | Explain no data vs no matches; reset filters or role-allowed create action |
| READY | Actions enabled only after data/scope checks |
| UNAUTHORIZED | No protected content; safe return/login action; server enforces 401/403 |
| CONNECTION_ERROR | Preserve draft/filter state, explicit retry of reads; execution outcome remains unknown until reconciled |
| Validation error | Inline field association + error summary/focus; do not clear valid input |
| Confirmation | Named resource/environment/consequence, clear Cancel/Confirm; focus trap and restoration |

Keyboard navigation, focus states, labels/status announcements, reduced motion and mobile list transformations require implementation accessibility testing. Static Stitch screenshots do not prove interactive correctness.
