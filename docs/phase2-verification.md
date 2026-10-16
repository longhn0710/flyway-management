# Phase 2 implementation and verification

Verified locally on 2026-10-16, Linux amd64. Phase 2 functionality is implemented; production deployment acceptance still requires the environment-specific checks below. No push, merge, deployment, external infrastructure change or live installation upgrade was performed.

## Delivered modules and API

- `internal/core`: local identity/bootstrap, password lifecycle, durable sessions, centralized target/schema policy, users/grants, service accounts/tokens, Oracle registry, credential administration, provider settings and audit.
- `internal/identityprovider`: verified LDAPS/StartTLS direct user bind and immutable subject lookup; OIDC HTTPS discovery/JWKS, authorization code + PKCE, validated ID tokens and nonce. Application callback processing adds single-use state, browser-cookie binding, expiry, configuration binding and explicit issuer + subject lookup.
- `internal/secrets`: AES-256-GCM local store behind a small adapter interface. Fresh nonces and record-bound authenticated data; separate private key file. Oracle credentials and provider configuration are encrypted; no secret readback API.
- `internal/httpapi`: versioned DTOs, server identity/role enforcement, CSRF + Origin validation, bounded JSON bodies, typed errors, deadlines, filtering/pagination and durable audit attribution. Administrative writes recheck the browser actor inside the transaction, including revocation after initial authentication.
- Existing React screens: real bootstrap/login/logout, profile password/session controls, user and scoped access administration, service token lifecycle, authentication providers, targets/schema streams/default accounts, execution accounts/mappings/credentials, and reviewable audit history in Users & Access. Principal changes clear query caches. Loading, empty, failed, denied, confirmation and one-time-secret states use the existing components/design tokens/navigation.

[OpenAPI 3.1](openapi.yaml) covers 43 implemented HTTP operations plus authenticated Phase 3 placeholders. Natural-key uniqueness rejects duplicate registrations/token names. Bootstrap is single-use; repeat rotation against an already revoked token returns 409. No jobs, artifacts, migration execution, Oracle provisioning or connection probes are implemented. The run screens contain explicit unavailable states rather than fixture results.

## SQLite changes and preserved behavior

Migration `002_identity_registry.sql` adds 13 tables: `users`, `sessions`, `database_targets`, `execution_accounts`, `target_schemas`, `account_schema_grants`, `credential_secrets`, `service_accounts`, `api_tokens`, `permission_grants`, `audit_events`, `auth_settings`, `oidc_flows`. It includes foreign keys, role/status checks, duplicate identity protection, unique target/schema history locations, same-target account mappings and grant constraints.

Installation metadata gains a one-time bootstrap marker and encrypted key check. Migration 001, the canonical product specification and the Stitch project remain unchanged. The existing checksummed migration ledger, legacy ledger upgrade, independent schema checkpoint, SQLite WAL/full synchronous durability, restrictive files, crash recovery, graceful shutdown and embedded assets remain operational. Test fixtures were extended to the new schema version without weakening the original drift/rollback checks. Phase 1 illustrative business workflows have been replaced by actual Phase 2 data or honest Phase 3 unavailability.

## Authentication and authorization model

Local passwords: Argon2id, 64 MiB, three iterations, two lanes, random 16-byte salts; minimum 12 bytes, maximum 1,024. Address/username login limits are ten attempts per minute and use bounded in-memory keys. Generic errors cover missing, disabled, wrong-password and unmapped external users. Provider selection never silently falls back.

Browser sessions: opaque random secret hashed at rest, HttpOnly/SameSite=Lax cookies, Secure under direct/configured HTTPS; 12-hour absolute and 30-minute idle expiry. Logout, disable, role changes and password changes revoke sessions. Own-session revocation does not allow cross-user IDs. Cookie mutations require matching Origin and CSRF; initial password login/bootstrap require matching Origin as well. Forwarded identity/IP/protocol headers are not trusted.

Admin has global registry/identity administration and metadata access. Operational access for every identity requires grants; Viewer cannot operate. Target grants cover all schemas, schema grants cover one stream, and PROD operations additionally require explicit `prod`. Changing a target environment clears PROD flags and audits that permission change. Service accounts have active owners and scoped read/operate permissions, never administrative web sessions. Disabling/transferring an owner or disabling a service revokes its tokens permanently; reactivation does not restore them.

Successful mutations and their audit events commit atomically. Correlation IDs are generated by the server; diagnostic and denied-request logs use API route templates to exclude untrusted path values. Events include authenticated actor, action, resource, UTC timestamp, outcome and request ID; failed authentication/authorization and rejected mutation attempts are audited without request secrets. Audit has no HTTP modification API, but is not tamper-proof against a filesystem/SQLite administrator.

## Verification evidence

| Check | Result / coverage |
| --- | --- |
| `make check` | Production TypeScript/Vite build, static native Go binary, Go vet/unit/integration/native-process checks, ESLint and 11 frontend unit tests |
| Go race tests | All Go packages pass, including concurrent one-time bootstrap and controlled provider tests |
| Browser E2E | Seven Chromium tests: setup/session reload/logout; registry/schema/user grants; token issue/rotation/revocation; write-only credentials; provider display/revoked-session behavior; responsive routes; direct Admin→Operator identity transition without prior principal cache access |
| Native lifecycle | Isolated compiled processes preserve installation ID, sessions, registry and encrypted credential metadata across restart; wrong/missing keys stop startup; original key restoration works; private file modes and clean SIGTERM verified |
| Migration regression | Legacy data preservation, complete ledger validation, tampering/gap/checkpoint failures, transactional pending migration rollback/retry, symlink rejection, WAL recovery after abrupt child-process exit |
| Security-negative tests | Unauthenticated APIs, forged preview/identity headers, Operator/Admin and Viewer mutation boundaries, cross-user sessions, cross-target/schema grants, denied PROD, disabled/expired/revoked sessions and tokens, delayed writes after session revocation, CSRF/Origin, request size/type/unknown-field handling, audit failure rollback, secret response/log redaction |
| Credential tests | Randomized AES-GCM, ciphertext/nonce/AAD tampering, restart decryption, replacement/revocation, wrong/missing/public/symlinked keys, encrypted provider secret preservation/redaction |
| LDAP controlled providers | Actual local TLS LDAPS and StartTLS bind/search success, failed passwords, bad certificates, explicit application identity/session mapping, unregistered subject and no local fallback |
| OIDC controlled provider | Actual local HTTPS discovery/JWKS/code exchange with signed ID tokens; PKCE, nonce, issuer, audience, signature, lifetime, state, cookie binding, replay, absent/unregistered subject and disabled-user failures; insecure discovery endpoints/redirects rejected |
| `make security` | npm audit: zero findings. Go scan: zero reachable or imported-package vulnerabilities. One module-only advisory, GO-2026-5932, covers the unused/unimported `x/crypto/openpgp` package, not the Argon2 implementation used here |
| OpenAPI | Redocly validation passes; license metadata and mandatory-4xx style rules excluded because no license was assigned and public read-only routes need not invent 4xx outcomes |

The Go suite contains 59 top-level tests plus parameterized cases. Browser layout checks cover desktop and mobile without page overflow. Design handoff/screens were reviewed; no automated pixel-diff baseline or independent penetration test is claimed.

Independent security review found owner-token resurrection, a last-enabled-admin lockout path and an oversized current-password hashing input. These were fixed and have regression tests. Further review found no blocking defect in transactional session rechecks, provider-setting concurrency protection or HTTPS redirect handling. No known reachable high-severity finding remains from this review; this is not an assertion that all vulnerabilities are absent.

## External verification gaps and Phase 3 readiness

No live AD, Entra ID, Keycloak or Oracle endpoint was supplied or tested. Local protocol fixtures establish application/protocol behavior, not vendor-environment compatibility. LDAP DN/attribute/subject mappings and CA chains, OIDC app registration/claims/callback/proxy configuration, and actual Oracle connectivity/privileges still need acceptance against the intended systems.

Remote HTTPS/reverse-proxy behavior was tested through configured-origin/direct-TLS handler cases, not a deployed ingress. A real remote installation must set `public_url`, terminate trusted TLS, restrict direct HTTP backend access, and validate the network/transport configuration. Address limits intentionally use the direct peer; clients behind one proxy share its limit.

Master-key backup/restore, storage/power-loss drills, recovery administration, capacity/retention and non-Linux installation/packaging need operational acceptance. Back up SQLite and the matching separate key; missing keys are not recoverable by regeneration. MFA, password reset/recovery tooling, a Vault adapter and multiple OIDC/LDAP configurations per provider type are not implemented. One configured LDAP and one OIDC provider are supported, with explicitly registered identities and no auto-provisioning.

The identity/registry/credential foundation is ready for Phase 3 implementation. Real execution must remain disabled until the canonical Oracle/Flyway compatibility, immutable bundle continuity, per-stream serialization, credential injection, failure/repair safety and ambiguous-outcome gates are verified. No production-readiness sign-off is inferred solely from passing local tests.
