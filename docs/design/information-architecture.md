# Flyway Manager — Information architecture

Source: [canonical product specification](../../flyway-manager-product-spec.md), sections 3–9. Design scope only; technical acceptance gates in section 10 remain open.

## People and identities

| Persona | Main journey | Authority |
|---|---|---|
| Operator | Select granted stream → upload bundle → validate → confirm → monitor → inspect failure/repair | Upload and run only on granted database/schema; PROD requires explicit grant |
| Viewer | Home history → filters → run → logs/repair evidence | Read granted metadata and logs; SQL download is separately authorized |
| Admin | Register Oracle service/PDB → schemas/history → reusable execution account → grants → providers | Manage registry, credentials, access and repair policy; PROD execution still explicit |
| API client | Scoped service token → artifact → submit with idempotency → poll/SSE → terminal outcome | Service identity is authoritative; caller metadata is provenance only |

Human application user, automation service account and Oracle execution account are different resources. A schema is a migration target, not necessarily a login account.

## Navigation

Login has Local / LDAP choices and an OIDC SSO action, with no API token input. Successful login opens **Lịch sử chạy** (home).

Shared 216px sidebar: Lịch sử chạy; Chạy migration; Database & Schema; Tài khoản thực thi; Người dùng & Quyền; Service Accounts; Cài đặt. Admin-only management entries are hidden from other roles. Viewers can inspect authorized database/schema information without mutation controls. User menu contains identity/provider, role, sign out. No fleet/node dashboard.

| Route contract | Primary screen | Navigation |
|---|---|---|
| `/login` | S01 Login | Return to validated same-origin deep link after auth |
| `/runs` | S02 Run History | Default home; URL retains filters and pagination |
| `/runs/new` | S03 Target/schema/account + bundle | Next after normalization succeeds |
| `/runs/new/review` | S04 Validation + confirmation | Back preserves draft; submit creates run |
| `/runs/:id` | S05 Running / S06 Failed + repair | Same route changes by status; return preserves history filters |
| `/databases` | S07 Database registry | Open target details; Admin registration dialog |
| `/databases/:id` | S08 Database details | Schema stream/config drawer; linked account and run history |
| `/execution-accounts` | S09 Credential registry | Register/rotate and map authorized schemas |
| `/access/users` | S10 Users & Access | Role/scope editor drawer |
| `/access/service-accounts` | S11 Service Accounts | Issue once, scope, expiry, rotation/revoke dialogs |
| `/settings/authentication` | S12 Authentication settings | Local, LDAP/AD, OIDC tabs; Admin only |

## Critical journeys and invariants

Design fixture consistency for linked views (illustrative snapshots, not real execution): RUN-1048 = WEB human Nguyễn Minh Anh/usr_17, ORA_UAT_FINANCE/FINUAT/UAT + CORE_BANKING, DEPLOY_ADMIN, ART-82FD21/finance_v3.zip, started2025-05-19 14:32:10, RUNNING with pre-run versionV2. RUN-1047 = API service ci-finance-uat, caller metadata pipeline-1047 untrusted, same stream/account, ART-82EB19/finance_v3.2.0_patch_idx.zip, started2025-05-18 22:15:02,36s, FAILED + REP-204 repairSUCCEEDED + NEEDS_REVIEW. These depict different operational moments; a later run cannot dispatch on an unreconciled blocked stream. The newer run is a new intentional artifact after verified reconciliation, never automatic SQL retry.

1. Register target **service/PDB** → register schemas → configure unique physical history location per stream → register an Oracle execution account → explicitly associate one or more schemas. No schema/database provisioning.
2. Choose exactly one `(target_id, schema_id)` → choose mapped execution account → upload SQL files/folder/ZIP/TAR → inspect manifest and historical continuity → confirm frozen target/config/artifact → async queue → logs. No approval queue.
3. Failure → persist immutable migrate error/audit → safety evaluation → conditional repair if eligible → repair evidence. Migration remains FAILED even when repair succeeds. Unsafe/uncertain outcomes hold the stream BLOCKED/NEEDS_REVIEW; no manual repair, rollback or retry action.
4. API client uses published REST resources and the same permissions/state model. Tokens never appear in browser login. No vendor-specific CI integration screen.
5. First launch is a protected bootstrap dialog before normal login, documented in interaction contracts; no extra primary screen required. API examples live within Service Accounts. These choices keep the set at 12 primary screens.
