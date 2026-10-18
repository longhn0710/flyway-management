# Phase 9 — Enterprise integrations

Plan and read-only discovery dated **2026-10-18 (Asia/Ho_Chi_Minh)**. This is the first repository copy of the Phase 9 plan previously discussed in ChatGPT, incorporating the user's Elastic Cloud update and subsequent scope clarification. It supersedes self-hosted ELK discovery/deployment requirements. Implementation kickoff is authorized; feature acceptance and deployment remain gated.

The approved feature groups are **Safe Delete Project**, **Enterprise Authentication — Keycloak OIDC and LDAP**, and **Elastic Cloud Observability**. Delivery order is Delete Project → Keycloak → isolated LDAP/federation → Elastic. Missing Elastic configuration blocks only Elastic ingestion; independent project and identity work can continue.

## Baseline and boundaries

Preserve the [Phase 8 remediation release](https://github.com/longhn0710/flyway-management/pull/5): source `f507dcaa543f10cddf2805f06b8169d83fd980d7`, image `ghcr.io/longhn0710/flyway-manager@sha256:1a0c80c1ef869a6f1735cdf7bbae9973ffd607c061a07966f2ee621c2690f8d5`, SQLite v8, installation `08c3533660b75625462d3ab3dd1f7071`. The live image was independently observed during this discovery; other acceptance facts remain attributed to the operator's local Phase 8 acceptance report.

The current checkout is `feat/postgresql-foundation` at `f493003` with substantial tracked and untracked Phase 8 work. Preserve it. Before implementation, reconcile the intended changes against the accepted source in a separate clean checkout; do not implement from an assumed clean or current master. Implementation uses focused branches from the accepted source; the original checkout and its unrelated changes remain preserved.

Keep Flyway 13.10.3, its integrity manifest, CLI policy, PostgreSQL/Oracle execution semantics, immutable SQL/history, credentials, grants, PROD checks, journals, NEEDS_REVIEW and holds. No maintenance-command expansion, SQL replay, hold release or registry edit is included. SQLite remains authoritative; Elastic is secondary and must never participate in migration admission, execution or local audit commits.

The implementation kickoff authorizes local implementation, focused feature branches/PRs and their CI. Merge, deployment, shared Keycloak realm/client changes, new LDAP resources, collector/logging or NetworkPolicy changes still require explicit authorization of the concrete reviewed change. Do not create paid Elastic resources, change billing or install Elasticsearch, Logstash or Kibana. No feature is accepted or deployed solely on unit-test results.

## Read-only infrastructure and code discovery

| Area | Observed result | Consequence |
| --- | --- | --- |
| Kubernetes context | `k8s-admin-public` | Use the explicit context in operator commands. |
| Manager | Namespace/Deployment/Service/Ingress `flyway-manager`; live image matches the Phase 8 digest above | Proposed collector and API-key Secret belong in the verified application namespace. |
| Application configuration | `flyway-manager-config/config.json` contains artifact, data, Flyway, listener, logging, master-key and public-URL configuration keys; no Elastic/OTLP key | No verified export configuration exists in this ConfigMap. Encrypted application authentication settings were not queried. |
| NetworkPolicy | `flyway-manager` allows ingress from ingress-nginx on 8080; egress to kube-system DNS and the designated Oracle lab on 1521. `allow-postgresql-phase8-lab` adds the designated PostgreSQL lab on 5432 | Current policies contain no Keycloak, LDAP or external Elastic egress allowance. Do not claim Manager-to-provider connectivity. |
| Cilium policies | `flyway-manager-host-ingress` permits host/remote-node ingress on 8080; no clusterwide Cilium policies returned | Include Cilium and ordinary Kubernetes policies in future rendered/network review; do not widen unrelated access. |
| Keycloak workload | Namespace/Deployment/Service `keycloak`; one ready replica, configured image `quay.io/keycloak/keycloak:26.7.0` | Reuse the installation after approval; an image tag alone is not runtime digest verification. |
| Keycloak ingress | TLS hosts `auth.apps.drgdevlab.com` and `database-manager-auth.apps.drgdevlab.com` | Use the discovered canonical issuer, preserving certificate verification. |
| Keycloak realm discovery | Bootstrap ConfigMap identifies realm `kubernetes`; public discovery returned HTTP 200 with verified TLS at `https://auth.apps.drgdevlab.com/realms/kubernetes/.well-known/openid-configuration`; issuer is `https://auth.apps.drgdevlab.com/realms/kubernetes`; S256 advertised | Existing shared realm is discoverable. This proves neither a Flyway client registration nor successful application authentication. |
| Keycloak client | No Flyway-named client found in the inspected bootstrap ConfigMap; live Admin API/client inventory not inspected | Client identity, scope and authorization remain prerequisites. Bootstrap configuration is not a complete live realm inventory. |
| Keycloak policies | No ordinary NetworkPolicy returned in namespace `keycloak` | Does not override Manager egress restrictions or prove a route from its pod. |
| Collectors | Deployment/DaemonSet/StatefulSet images and all 88 observed pods' container/init-container names and images yielded no recognizable OTel, Elastic Agent, Filebeat, Fluent Bit/Fluentd, Vector, Alloy or Promtail agent. No observability-related CRDs returned | No approved reusable collector was established. Host-installed agents and external collectors are not excluded by this inventory. Older GitOps inventory mentioning an OTel operator is historical, not live proof. |
| LDAP | No recognizable OpenLDAP workload in the inspected workload inventory | Prepare an isolated lab design; do not reuse another service or deploy resources under this task. |
| Elastic Secret | Secret-name/type inventory returned no Elastic/OTLP-named Secret; proposed `flyway-manager/flyway-manager-elastic-api-key` was absent | Name is proposed, not provisioned. No key values were displayed or decoded. |
| Elastic account/project | User supplied **My Observability project**, Elastic Cloud Observability, GCP `asia-southeast1`. Kibana HTTPS returned HTTP 200 after the Cloud SAML entry redirect; supplied OTLP host returned HTTP 400 to an unauthenticated root GET with certificate verification | Kibana URL/connectivity confirmed from the operator host; OTLP TLS reachability observed, but valid OTLP request/authentication, pod connectivity, indexing and actual Serverless/Hosted account configuration remain unverified. Trial/cost/retention remain **UNVERIFIED**. |

Discovery used `kubectl get` for workloads, pods, Services, Ingresses, ConfigMaps, Secret metadata, NetworkPolicies and Cilium policies/CRDs, plus a public HTTPS GET for OIDC discovery. No pod exec, login, resource write or shared-realm mutation was performed. ConfigMap output was restricted to configuration keys and selected non-secret realm metadata; neither Secret values nor complete realm configuration belong in evidence.

Existing implementation to reuse:

- [Project service](../internal/core/projects.go), [project routes](../internal/httpapi/projects.go) and [project schema](../internal/database/migrations/005_projects.sql): create/update/archive and current-membership history exist; no Delete Project route. Project membership references targets/schemas; physically deleting a project row would cascade membership rows. Historical runs are scoped by target/schema, rather than owned exclusively by a project.
- [Provider adapters](../internal/identityprovider/provider.go), [authentication settings](../internal/core/settings.go) and [identity service](../internal/core/identity.go): OIDC Authorization Code + PKCE S256, state/nonce/cookie checks, verified TLS, LDAP secure bind and explicit preprovisioned identities already exist. Application roles/grants are local; provider groups do not currently establish application authority. Reuse these paths instead of introducing a second login/session system.
- [Audit writes](../internal/core/types.go), [audit API](../internal/core/tokens.go), [recovery events](../internal/core/execution_jobs.go) and [HTTP diagnostics](../internal/httpapi/router.go): audit rows include actor/action/target/time/outcome/request ID; execution state has additional persisted events. The server already uses JSON slog, but existing request logs are not a complete versioned audit export. Some recovery paths write audit rows directly, so a single mutation callback cannot cover every event.

## Focused PR sequence and acceptance gates

Each PR includes its own API/UI/configuration documentation and acceptance evidence. The two identity PRs are separate increments of the same approved feature group. PR labels below are proposed work units, not existing GitHub PRs. All live mutation tests use approved disposable fixtures; no original project is deleted and no existing account/grant is repurposed.

### PR 9.1 — Safe Delete Project

**Implementation contract:** Admin-only deletion with exact `confirmation_name` matching the current persisted project name. Reject **all** remaining membership rows, including inactive resources and archived projects, with HTTP 409. Archived membership stays read-only: unarchive, explicitly remove membership, then delete. Cancellation performs no request. Missing projects and repeated deletion return 404 with no extra successful deletion audit.

Repository inspection establishes that only `project_members` references projects; runs/artifacts/history are target/schema-scoped, and audit targets are retained text identifiers. Therefore an empty project's metadata can be physically removed without destroying historical references. No SQLite migration is needed. Recheck this reference graph in review; use a tombstone instead if a historical project reference is introduced. Never delete any membership, instance, schema, account, credential, grant, artifact, run, capture, log, journal or prior audit as a side effect. Deletion issues no Flyway/database-target command.

Check Admin authority, current name and absence of membership within the existing immediate write transaction, together with deletion and its `project.delete` success audit. Reuse transactional session/role revalidation. A racing membership update must either commit first and prevent deletion or observe the deleted project and fail. Do not trust the UI's membership count. The UI explains the blocked state and preserves existing history navigation/permissions.

**Acceptance gate D:** real local API/UI/browser tests for Admin success, Operator/Viewer/service denial, CSRF, malformed/wrong confirmation, cancellation, stale rename, duplicate requests, racing membership updates and archived/nonempty/inactive-resource behavior. Inject audit failure to prove rollback. Validate integrity/foreign keys and compare immutable rows/bytes before/after, including complete succeeded-run history and shared resources. Browser acceptance covers desktop/mobile confirmation, lists, deleted deep links and retained run access. Original live projects and external databases are untouched. Required CI must pass; merge/deployment stay gated.

### PR 9.2 — Keycloak OIDC SSO

Retain the discovered shared Keycloak installation. First obtain approved realm/client ownership and inspect the actual client read-only with the realm administrator. A dedicated Flyway client or isolated acceptance realm is a proposal requiring approval, not permission to edit `kubernetes`. Do not alter existing clients, realm login behavior, users or groups.

Use the existing confidential web-client adapter with Authorization Code, PKCE **S256**, state, nonce, one-use expiring flows and issuer/audience/signature/expiry verification. Proposed exact callback, based on the established Manager origin, is `https://flyway-manager.apps.drgdevlab.com/api/v1/auth/oidc/callback`; confirm against deployed `public_url` and registered redirect URI before enabling. No wildcard redirects, implicit grant, password grant, TLS bypass or credential-bearing URLs. Transport client secrets through an approved protected mechanism and the existing encrypted settings path; never commit them or expose them in settings responses, logs or HAR evidence.

Preserve enabled local login and a tested local Admin account throughout. Use separate explicit `(provider, issuer, subject)` registrations with locally assigned Viewer/Operator/Admin roles and target/schema/PROD grants. Matching email or username never links accounts. Default to no access for an unregistered subject. Any group mapping must be an explicit, reviewed allowlist for the exact issuer and approved groups, with unknown/absent/conflicting groups denied and group removal tested. Do not add automatic Admin provisioning or treat arbitrary token claims as grants.

Review the narrow Manager DNS/HTTPS route needed for discovery, JWKS and token exchange. Public discovery from the operator host does not prove pod reachability. Preserve the canonical HTTPS issuer even if an approved internal route is used. No ingress or egress policy change is authorized by discovery alone.

**Acceptance gate K:** approved real Keycloak client, verified issuer/JWKS and pod route; complete browser SSO, logout/local session termination and local Admin fallback during provider outage. Test denied/unregistered/inactive subjects, wrong issuer/audience/nonce/state, expired token, replayed code/flow, bad PKCE, redirect mismatch, untrusted certificate, disabled provider, provider timeout and configuration changes during a flow. Prove explicit RBAC and PROD denial, absence of identity collision or role escalation, existing session revocation behavior, and no stored/displayed tokens or client secrets. Local fixture tests supplement the live Keycloak browser/API evidence; discovery alone is not acceptance.

### PR 9.3 — Isolated OpenLDAP and federation acceptance

Prepare reviewable, pinned-image manifests and capacity/security checks for a **new isolated OpenLDAP lab**. Deploy only after explicit authorization. Require internal ClusterIP, no public Ingress/NodePort/LoadBalancer, secure LDAPS or StartTLS with verified certificates, protected credentials, default-deny networking, no automatic service-account token, nonprivileged runtime where supported, resource/storage bounds and disposable users/groups. Restrict connections to designated Manager/Keycloak acceptance workloads; deny unrelated pods. Do not reuse another application's directory or credentials.

Test both existing direct Manager LDAP authentication and Keycloak LDAP user federation → Manager OIDC. Add the federation provider/group mapper only in an explicitly authorized isolated acceptance realm; never modify a shared realm implicitly. Use stable LDAP identity attributes and exact issuer/subject mapping. Direct LDAP identities and federated OIDC identities are distinct unless separately mapped by an administrator. Map named lab groups to explicitly reviewed application identities/roles/grants; no default or inferred Admin access. Record whether mapping is administrative or automated, and the exact group-change/revocation behavior.

**Acceptance gate L:** real bind/search, immutable subject, escaped username/DN/filter handling, correct and incorrect password, unknown/inactive user, ambiguous lookup, untrusted/expired certificate, plaintext rejection and directory outage. Prove federation login and group mapping with Viewer/Operator/Admin fixtures, unknown/removed groups denied, disabled user and session/relogin behavior, distinct identities across providers, scope/PROD enforcement and preserved local Admin access. Verify unauthorized-pod and public-access denial. Retire test users/tokens without deleting retained audit evidence. Lab topology/TLS/identity evidence is required; simulated LDAP protocol tests cannot establish federation acceptance.

### PR 9.4 — Elastic Cloud events, scoped ingestion and dashboard

**Status: blocked at Secret-based authentication, before ingestion.** Complete the contract/preparation described below; do not export events, provision Elastic, deploy a collector or create dashboards until its project/destination and key reference are verified and the change is authorized. Keep PRs 9.1–9.3 independent.

#### Destination verification and external references required

| Required reference or observation | Current state / responsible party |
| --- | --- |
| Elastic Cloud console project/deployment URL and ID; actual Serverless or Hosted type | Supplied name **My Observability project**, product Elastic Cloud Observability. Console identity/ID and Serverless/Hosted classification still require account verification. |
| Observability/Kibana URL; Elasticsearch URL where available; region/cloud and version if Hosted | Confirmed Kibana URL: `https://my-observability-project-d9be29.kb.asia-southeast1.gcp.elastic.cloud`; verified TLS/HTTP 200 at its Cloud SAML entry. Supplied region: GCP `asia-southeast1`. Elasticsearch URL/version not supplied. |
| Managed OTLP endpoint, supported protocol/port and log-ingestion availability | Supplied `https://my-observability-project-d9be29.ingest.asia-southeast1.gcp.elastic.cloud:443`. Operator-host root GET reached the TLS-verified server (HTTP 400); actual OTLP connectivity/ingestion/authentication and pod egress remain pending. Do not substitute Kibana/APM/Elasticsearch URLs. |
| Authorized destination dataset/namespace and collector topology | Proposed Flyway-only dataset/namespace; owner approves destination and reviewed scoped collector. No existing approved collector established. |
| API-key reference | Proposed namespace `flyway-manager`, Secret `flyway-manager-elastic-api-key`, data key `api-key`; still absent at implementation kickoff (metadata-only check). Operator provisions securely, then supplies name/namespace/key reference only. |
| API-key scope/expiration/rotation owner; query/dashboard access | Unverified; ingestion and search are distinct permissions. Operator retains credentials and supplies redacted verification or approved read access. |
| Trial/subscription status, expiration, ingestion/rate/payload quotas, supported retention and agreed cost envelope | Unverified; inspect the actual account before export. No billing modification or paid resource creation. |

Elastic documents managed OTLP for Cloud Serverless and Hosted, OTLP log transport, and endpoint discovery through the Cloud console. Verify the actual project supports it. Its documentation also distinguishes transport acceptance from later indexing and notes that managed inputs do not support index-level key scoping. Treat that privilege boundary as part of destination review. [Managed OTLP documentation](https://www.elastic.co/docs/reference/opentelemetry/managed-inputs/managed-otlp-endpoint), [authentication and delivery](https://www.elastic.co/docs/reference/opentelemetry/managed-inputs/authentication-delivery-and-failure-handling).

If managed OTLP is unavailable, stop this workstream for an explicit architecture decision. Do not silently install a replacement stack or change protocols/credentials.

#### Operator-only Secret provisioning

After verifying the project, use its managed OTLP Add data flow to create a dedicated expiring ingestion key with only the documented required privileges. Copy its **encoded key value**, without the `ApiKey ` prefix, into the hidden prompt below. The command creates the proposed Secret in the verified existing namespace; it neither updates an existing Secret nor deploys a collector. Execute only in a trusted operator terminal, after authorization, with session recording disabled. Do not paste the key into chat, Git, shell arguments, YAML or evidence. API-key creation is subject to the verified project's privilege model above.

```bash
(
  set -euo pipefail
  set +x
  trap 'unset FWM_ELASTIC_KEY' EXIT
  FWM_EXISTING_SECRET=$(kubectl --context k8s-admin-public --request-timeout=15s \
    -n flyway-manager get secret flyway-manager-elastic-api-key \
    --ignore-not-found -o name)
  if [ -n "$FWM_EXISTING_SECRET" ]; then
    printf '%s\n' 'Secret already exists; use the separately reviewed rotation procedure.' >&2
    exit 1
  fi
  IFS= read -r -s -p 'Encoded Elastic ingestion API key: ' FWM_ELASTIC_KEY
  printf '\n' >&2
  test -n "$FWM_ELASTIC_KEY"
  printf '%s' "$FWM_ELASTIC_KEY" | kubectl --context k8s-admin-public \
    --request-timeout=15s -n flyway-manager create secret generic \
    flyway-manager-elastic-api-key --from-file=api-key=/dev/stdin
)
```

Verify existence through metadata only. A collector will reference the Secret through a protected mount or `secretKeyRef`; the value never belongs in a Deployment, ConfigMap, rendered Helm diff or Git. Mount/inject it only into the exporter, not Manager or Flyway subprocesses. Kubernetes Secret encryption at rest remains unverified under Phase 8 and is not established by this procedure.

#### Event contract and safe source mapping

Use a typed, versioned projection of **committed** Manager audit/execution events. Never send a database row, request object or CLI result wholesale. Cover normal mutations and direct recovery/state audit writes. Define stable `event_id` from installation identity plus persisted source kind/ID; the same source event retains its ID across retries. No SQL/history semantics change is needed. Use fixed action/outcome/severity dictionaries; reject malformed or oversized fields and drop unknown fields before transport.

| JSON field | Type and mapping |
| --- | --- |
| `schema_version` | Integer, initially `1`; explicit compatibility policy for future versions. |
| `event_id`, `event_type` | Bounded keyword strings; deterministic source identity and allowlisted audit/execution category. |
| `timestamp`, `severity` | UTC RFC3339 timestamp mapped to OTLP log time / indexed date; allowlisted severity mapped to OTLP severity. Use persisted event time, not export time. |
| `project_id`, `instance_id`, `engine`, `database`, `schema` | Bounded keyword identifiers from explicit persisted context or frozen run/target identity. `instance_id` maps to Manager target ID; engines are allowlisted. |
| `run_id`, `correlation_id` | Bounded keywords; persisted run ID and server-generated request/correlation ID. Do not accept arbitrary client headers as identity. |
| `actor_id`, `authentication_provider` | Opaque internal actor ID and allowlisted `local`/`oidc`/`ldap`/`service`/`system`/`unknown`. Failed unknown authentication has no invented actor. No usernames, email, DN, raw subject, groups or token claims. |
| `action`, `outcome` | Bounded allowlisted keywords derived from persisted facts; maintain distinct failure, denial, NEEDS_REVIEW and held-state meanings. |
| `duration_ms` | Optional nonnegative integer milliseconds from recorded execution timing; never infer duration from export latency. |

The schema defines all fields; omit unavailable optional context rather than emitting empty invented identifiers or changing types. Many existing audits have only a polymorphic target and no project/provider context. Join only through known action-specific IDs and reliable persisted/frozen context. Never infer a historical project from today's membership or re-label a historical provider from a changed identity record. Document unavailable fields; add narrowly reviewed safe context capture only if needed. Authentication/authorization denial coverage must be established explicitly, not assumed from success audits.

OTLP resource attributes are fixed: `service.name=flyway-manager`, `deployment.environment=homelab`, plus verified `service.version` and `service.instance.id` when available. Propose `data_stream.dataset=flyway_manager.events` and `data_stream.namespace=homelab`; verify their actual indexed paths and destination before enabling. Preserve these resource attributes separately from per-event fields. Keep searchable IDs as keywords and durations/version as numbers. Elastic's OTLP documents may retain fields under resource/log attribute paths; acceptance must record the actual paths and queries instead of assuming ECS aliases.

Explicit exclusions: passwords, tokens/API keys, cookies, authorization headers, OIDC codes/verifiers/state/nonce, private authentication attributes, credential ciphertext, JDBC/credential-bearing URLs, raw SQL/artifacts and full requests, stack traces or arbitrary Flyway stdout/stderr. String truncation or regex replacement alone is not redaction. Use a producer allowlist plus an independent collector allowlist; unrecognized bodies/fields are dropped. Secret-canary tests must cover ordinary, denied, error and recovery events.

#### Scoped collector architecture and bounded failure behavior

Preferred flow: **committed Manager event → allowlisted structured JSON → approved existing collector or small Flyway-only OTel Collector → verified Elastic managed OTLP → indexed search/dashboard**. Recheck for an approved reusable collector before adding one. None was established by this discovery.

Proposed dedicated fallback is a sidecar filelog receiver reading only a dedicated sanitized event stream in a bounded, memory-backed shared volume. Manager's background projector writes that stream separately from generic request/process/CLI logs. The collector never mounts SQLite, keys, SQL, credentials, node logs or host paths, and has no Kubernetes log-discovery permission. No clusterwide DaemonSet or instrumentation. A loopback-scoped diagnostic endpoint may expose aggregate counters without payloads; it is not public.

Proposed starting bounds, to be validated against a pinned collector distribution: 4 KiB maximum event; asynchronous producer queue of 1,024 events; two rotated 4 MiB sanitized files in a 16 MiB tmpfs; collector queue bounded to 8 MiB, batches bounded to 256 KiB/1 second; 10-second export timeout and capped exponential retry for up to 60 seconds per batch. Collector request 50m CPU/64 MiB and limit 200m CPU/256 MiB, with memory-limiter headroom and tmpfs memory accounted for. Component capabilities and exact settings require configuration validation; these are proposed values, not measured capacity or deployed settings.

The application transaction and migration worker never wait for file writes, collector readiness or external requests. Projection uses bounded read batches/short read-only queries outside write transactions; failures, full queues and rotations increment local aggregate drop/error counters and continue. Collector backpressure must terminate at this background boundary. Test that database reads do not starve execution or backup. No persistent telemetry spool or sensitive payload-on-error logging. Restart/drop behavior is documented rather than claiming exactly-once delivery.

On transient timeout/429/5xx use bounded retry with jitter; authentication/TLS/schema failures surface a sanitized diagnostic without unbounded retry. Inspect OTLP partial-success responses and asynchronous indexing failures. On recovery, resume new export and drain bounded retained records. A bounded read-only re-projection of persisted source IDs may recover gaps only with a documented cursor/window/rate limit and explicit authorization for historical export. Never replay SQL or re-emit an unlimited audit history.

Duplicate records are possible after an ambiguous response/restart; deterministic `event_id` enables measured duplicate counts and dashboard cardinality. Do not claim the managed endpoint deduplicates or offers exactly-once semantics. Records can be lost to queue overflow, retry expiry, rotation, restart or downstream indexing rejection; reconcile a controlled expected source-ID set with indexed distinct IDs and record missing/duplicate counts and reasons. Expose exporter health locally; local authoritative audits remain retained regardless of export gaps.

Run the collector nonroot with dropped capabilities, read-only root and disabled service-account token. Use HTTPS with certificate/hostname verification and a dedicated Secret-backed key; no insecure fallback/debug body exporter. Propose narrow DNS and destination HTTPS egress for the collector only. Review FQDN-aware Cilium/proxy options for the verified dynamic Elastic hostname; ordinary NetworkPolicy cannot constrain a hostname. Do not substitute unrestricted Internet egress or alter Manager's Oracle/PostgreSQL routes.

#### End-to-end acceptance, search and dashboard

**Acceptance gate E requires actual indexed events**, not only collector startup, mocked HTTP or an OTLP success response:

1. Perform an approved controlled Manager action in disposable fixtures. Record the authoritative local audit/source ID, timestamp, action/outcome and run/correlation ID without sensitive payloads.
2. Correlate the projected event and collector receive/export counters; retain redacted transport status and exact destination identity.
3. Query the actual Elastic destination by `run_id` or `correlation_id`; retain query, time window, indexed document/event ID and response evidence. Demonstrate collector receipt, Elastic acceptance **and indexing** separately.
4. Verify actual field paths/types with mapping or field-capability evidence: date, keyword IDs and numeric version/duration. Inspect the full controlled document for excluded fields and secret canaries without exposing real secrets.
5. Generate success/failure, authentication/authorization denial and NEEDS_REVIEW/HELD examples only in isolated fixtures. Prove no unrelated application's logs or generic Manager CLI output enter the stream.
6. Interrupt only the approved export connection/collector. Prove a permitted migration completes, its local audit commits and API/login remain usable within agreed bounds. Exercise queue saturation, retry expiry, collector restart and restoration; record timing, resource bounds and safe resumption.
7. Compare expected source event IDs against indexed distinct IDs before/after retry and restoration. Report duplicates, gaps, limits and any failure-store/indexing errors. Do not turn collector success counters into a claim of complete audit delivery.

Deliver one small saved Kibana dashboard scoped to the approved Flyway dataset and environment, with time/project/instance/database/schema/run/correlation filters. Panels: migration success/failure; activity over time; NEEDS_REVIEW/HELD transitions; authentication and authorization denials; instance/database/schema activity; run-level event timeline with safe links to Manager details. A transition count is not current held-stream inventory; label it accordingly. Use distinct source event IDs to avoid inflated retry counts, and document any approximate cardinality. No SQL or private identity data appears in panels. Retain dashboard export/ID, search evidence and inspected screenshots; verify dashboard viewer permissions separately from Manager RBAC.

#### Cost and retention gate

Before ingestion, the Elastic owner verifies actual trial/subscription status and expiry, supported retention controls, ingestion/payload/rate limits and an agreed spending/volume envelope. Public trial documentation is not evidence that this account is on trial. [Trial guidance](https://www.elastic.co/docs/get-started/evaluate-elastic), [Observability billing dimensions](https://www.elastic.co/docs/deploy-manage/cloud-organization/billing/elastic-observability-billing-dimensions).

Propose **7 days** retention for this Flyway-only non-production event dataset, subject to the actual product's supported minimum and owner approval. Verify the effective dataset/data-stream policy rather than assuming project defaults or self-managed ILM applies. Measure encoded event size and enriched metered volume during a small acceptance window; record daily event/byte estimates, backfill cost and trial end date. Exclude health/request noise, SQL and full stdout/stderr. Bound intake and surface local drop counters if the agreed budget is exceeded. No automatic paid upgrade, retention/billing change or account-wide policy change.

## Review, release and completion

For each implementation PR run the relevant existing checks from [Verify and publish](../.github/workflows/verify-publish.yaml): `make check test-race test-e2e security`, plus meaningful feature-specific API/browser/protocol and disposable upgrade/restore tests. Review OpenAPI compatibility, dependency/configuration diffs and credential-canary scans. Feature verification is recorded in the associated acceptance report; this plan alone does not establish test results.

Deployed acceptance requires separate authorization, required PR review/CI, a CI-published immutable source/image identity, a consistent fresh SQLite/auxiliary backup and separately protected original key, exact-image isolated restore and reviewed GitOps diff. Preserve original data, credentials, grants, SQL/history and holds; verify HTTPS, image, installation/PVC/key identity and idle state after promotion. Schema additions require the existing forward-upgrade/restore rules; do not roll an older binary over a newer SQLite schema. Follow [homelab operations](homelab-operations.md).

Current outcome at kickoff: baseline repository/Kubernetes discovery and supplied Elastic URL checks are complete; Delete Project implementation proceeds first, with Keycloak independent of Elastic. Feature status must be supported by its PR/acceptance evidence; this plan alone establishes no implemented or accepted capability. LDAP deployment/shared-realm edits await authorization. Elastic ingestion remains blocked on the missing securely provisioned Secret, authorized collector/destination and actual account cost/retention observations. Real ingestion, indexed search and pod-level connectivity are not yet proven. Preserve this distinction in PR descriptions and acceptance reports.
