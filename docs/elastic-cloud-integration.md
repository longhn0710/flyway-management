# Elastic Cloud Manager events — Phase 9

Status: implemented locally; **external ingestion NOT_VERIFIED**. The user authorized scoped Collector/network/TLS/authenticated OTLP acceptance on 2026-10-18; the existing Manager deployment/configuration remains outside that authorization. This feature adds an optional secondary destination. SQLite remains authoritative. It changes no migration, recovery, hold, credential or schema semantics. The existing deployment stays untouched.

## Destination and current evidence

The supplied project is **My Observability project**, Elastic Cloud Observability, GCP `asia-southeast1`. Kibana is `https://my-observability-project-d9be29.kb.asia-southeast1.gcp.elastic.cloud`; the managed OTLP origin is `https://my-observability-project-d9be29.ingest.asia-southeast1.gcp.elastic.cloud:443`. Previous operator-host checks verified TLS connectivity: Kibana HTTP 200 after Cloud login redirect; unauthenticated OTLP root HTTP 400. Neither proves authenticated OTLP or indexed search.

Metadata-only inspection on 2026-10-18 found `flyway-manager/flyway-manager-elastic-api-key` with both expected data entries. The current operator-approved contract uses **`OTEL_EXPORTER_OTLP_HEADERS` containing the full SDK-style header string**; the older `api-key` entry is unused. No Kubernetes Secret value was displayed or decoded outside its authorized workload. Extra Secret entries are unused. The auth initializer consumes only `OTEL_EXPORTER_OTLP_HEADERS` through `secretKeyRef`; Manager never receives the Elastic credential. The user subsequently identified the product as **Elastic Cloud Serverless Observability**. Cloud project ID, account privileges, Elasticsearch query URL, trial status, quota and current retention remain unverified. The user authorized bounded authenticated OTLP proof using this writer key; indexed search must use a separate approved read-only Kibana session/search credential, without broadening writer privileges.

Elastic documents managed OTLP for Serverless and Hosted; transport acceptance can precede indexing failure, and managed inputs do not offer index-level API-key scoping. The operator must approve that actual privilege boundary. See [managed OTLP](https://www.elastic.co/docs/reference/opentelemetry/managed-inputs/managed-otlp-endpoint) and [authentication/delivery](https://www.elastic.co/docs/reference/opentelemetry/managed-inputs/authentication-delivery-and-failure-handling).

## Implemented event contract

`internal/observability` reads newly committed audit rows through a separate query-only, read-only SQLite connection. Startup requires matching installation identity and an existing regular registry. It starts at the current high-water mark, deliberately exporting no historical backlog. One-second polling reads at most 100 rows within a 100 ms context; rows close before queueing/export. No writes, vault reads, artifact access or native CLI calls occur.

The complete version-1 allowlist is:

| Field | Type / meaning |
| --- | --- |
| `schema_version` | integer, 1 |
| `event_id` | SHA-256 of installation identity plus persisted audit row ID; stable across retries |
| `timestamp` | UTC RFC3339, also OTLP event timestamp |
| `event_type`, `severity`, `action`, `outcome` | fixed enumerations; unknown actions/outcomes rejected |
| `project_id` | opaque ID for project actions, when known |
| `instance_id`, `engine`, `database`, `schema`, `run_id` | validated IDs and frozen execution scope when available |
| `correlation_id`, `actor_id` | validated correlation / opaque actor ID, when available |
| `authentication_provider` | local, oidc, ldap, service, system or unknown; never issuer/subject/claims |
| `duration_ms` | optional nonnegative integer for terminal runs, max 24 hours |

Not every event has every scope. Migration events do not infer historical project membership from today's mutable mapping. Provider enum comes from immutable user-provider metadata. Database/schema identifiers outside the conservative allowlist are omitted. Original Oracle snapshots without an engine discriminator remain recognized as Oracle only when valid JSON. Audit seconds limit duration precision to seconds.

No names, passwords, cookies, tokens, JDBC URLs, credential ciphertext, SQL/artifacts, full requests, CLI output, arbitrary errors, issuer/subject or private claims enter the event DTO. Resource attributes are fixed: `service.name=flyway-manager`, `deployment.environment=homelab`, installation `service.instance.id`, dataset `flyway_manager.events`, namespace `homelab`. No ambient resource detectors/headers/proxy credentials are used.

## Transport and failure behavior

Flow: committed audit → typed JSON event plus typed OTLP attributes → bounded application queue → standalone mutually authenticated HTTPS Collector → Elastic HTTPS managed OTLP. This supersedes the initial sidecar/filelog proposal: no shared log volume, host paths, generic logs or SQLite/key mount in the collector; no Manager-pod Internet egress. The collector independently removes unlisted log/resource attributes and replaces the body with `flyway-manager.event.v1`; typed attributes retain searchable context. Only holders of the dedicated client CA identity may submit, further constrained by workload NetworkPolicy. Namespace administrators with Secret/workload control remain trusted.

Export is disabled by default. Invalid configuration disables optional export with a sanitized warning; application startup continues. Configuration contains an HTTPS origin and trust/client-identity file paths, no Elastic credential. The HTTP client rejects redirects, verifies certificates/hostnames, requires TLS 1.2+, has no proxy fallback and bounds requests/responses to 256/64 KiB. It emits only `/v1/logs`.

Application queue: 1,024 records, batches 64, one-second interval, 10-second export deadline. Retry backoff starts at one second, caps at five seconds, has a 60-second retry ceiling additionally bounded by the 10-second export context. Collector memory limiter: 192 MiB + 32 MiB spike; bounded 8 MiB memory queue, one consumer, nonblocking overflow, batch 64, one second, 10-second attempts, retry ceiling 60 seconds. No disk spool. Exact pinned Collector is v0.163.1 digest in the manifest.

Local intake caps at 5,000 events or 10 MiB JSON per UTC day **per Manager process**; restart resets these bounds. This is no billing guarantee: OTLP/enrichment overhead, other account ingestion and duplicate retries may increase indexed volume. Suggested non-production retention is seven days, pending the owner's verified product options and cost approval. No retention/billing setting is changed by this PR. [Elastic trial](https://www.elastic.co/docs/get-started/evaluate-elastic), [Observability billing](https://www.elastic.co/docs/deploy-manage/cloud-organization/billing/elastic-observability-billing-dimensions).

Failures never enter the local write transaction or worker call path. Aggregate counters report projection, validation rejection, read errors, budget drops, transport acceptance and transport failure once per minute without payloads. Transport acceptance does **not** mean indexing success or full audit delivery. SDK queue overflow may drop events; counters are diagnostic, not complete delivery accounting. Invalid records advance the cursor; read errors retain it. Budget drops, queue/retry expiry and restart lose optional exports. Application/collector restarts discard buffers; enabling/restarting starts at the current audit maximum, so shutdown/startup gaps and late shutdown audits are possible. There is no historical replay, durable telemetry cursor, exactly-once guarantee or auto-recovery of missing records. Retried requests may duplicate stable `event_id` values; dashboards must account for duplicates. SQLite retains original evidence throughout.

## Local verification and remaining acceptance

Tests use private synthetic registries and TLS fixtures. They check new committed rows, WAL reads during a pending writer, read-only enforcement, batch bounds, stable IDs, enum/field redaction, typed protobuf wire attributes, ambient-header exclusion, verified mutual TLS, untrusted certificate rejection, daily budget/rollover and bounded shutdown. A real execution fixture completes migration and local audit during observed HTTP 503 export failure; simulated CLI execution does not establish deployed database acceptance. Restoring the local receiver proves retained/new events resume.

The opt-in `TestCollectorLiveMutualTLSAndIndependentAllowlist` executes the exact pinned collector with a private loopback TLS destination and synthetic key. It sends a controlled real Manager project action and an intentionally extra-field/body canary, verifies independent stripping, and rejects a client without its certificate. This is local collector evidence, **not Elastic acceptance**.

Run normal checks with `make check test-race test-e2e security`; run the collector fixture with:

```sh
FWM_TEST_COLLECTOR_IMAGE=otel/opentelemetry-collector-contrib@sha256:1175b03003053962012f5a231f5567f66a3e8fdb960c7835c0e48f7aa1555806 \
  GOTOOLCHAIN=go1.26.9 go test ./internal/observability -run TestCollectorLive -count=1
```

Verified locally on 2026-10-18: `make check test-race test-e2e security` passed (26 frontend unit tests; 42 browser tests passed, one existing opt-in PostgreSQL case skipped). Go vulnerability scanning found no affecting calls/packages; it reported one uncalled module advisory. npm audit reported zero vulnerabilities. `go mod verify` passed. The exact pinned collector live fixture passed; cluster server-side dry-run accepted all six proposed objects without persistence. No external credential was used by these tests. Generated unrelated browser screenshots are excluded from this change.

External acceptance remains mandatory after deployment approval:

1. Verify project identity/type, OTLP support, scoped ingestion privileges/expiry, account/trial/quota and permitted retention; verify Secret reference without outputting values.
2. Approve/render the exact collector, Cilium DNS/FQDN egress, dedicated TLS Secrets and Manager config/mount change. Preserve existing config, image, key/PVC and database policies. Record actual capacity and certificate SAN/EKUs/expiry.
3. From the approved workload prove DNS, hostname/CA validation, unauthorized-client rejection and secret-based Elastic authentication. Keep payloads/headers out of logs.
4. Produce a controlled action with a unique correlation ID and an authorized non-production run. Record source audit/event IDs and local commit; observe collector receipt/export without logging event contents or key.
5. Query Kibana/actual Elasticsearch by run/correlation/event ID and bounded time window. Record actual index/data stream and field paths, document ID, typed version/duration/IDs and classified-canary absence. HTTP 200/transport counters alone are insufficient; inspect partial-success/failure-store evidence where supported.
6. Interrupt only the approved export route, execute a separately authorized disposable migration, prove success and local audit, restore route and compare distinct event IDs with source IDs. Record retry expiry, drops, duplicates, restart gaps and measured resource/latency bounds.
7. Save a small dashboard in the verified Kibana space: success/failure, activity over time, NEEDS_REVIEW/held outcomes, authentication/authorization failures, instance/database/schema activity and run/correlation troubleshooting. Use actual discovered field paths; saved objects and dashboard identity remain pending.

No Elastic dashboard, real searchable event, authenticated ingestion, deployed outage proof, trial or retention acceptance is claimed here. See [deployment proposal](../deploy/observability/README.md).

Collector internal counters bind only to `127.0.0.1:8888` and can be inspected through an authorized pod port-forward. No Service or NetworkPolicy exposes this diagnostic listener; it has no event payloads. Receiver accepted/exporter sent/failed counters separate internal receipt and transport completion from indexing.

Namespace RBAC inspection found existing clusterwide administrators/editors and platform controllers can create workloads/Secrets. These principals can mount namespace Secrets and remain within the homelab trust boundary; no anonymous/authenticated-all writer binding or new privilege was introduced. Group membership was not independently audited.

The full header string is not inserted behind another `ApiKey` prefix. A pinned, nonroot Python init container validates the standard single `Authorization=...` OTLP header configuration inside the workload, accepts standard percent-escaped header syntax, rejects duplicate/extra headers and control characters, and reports only a fixed PASS/REJECTED marker. It writes the normalized header map to a mode-0600 file on a bounded 1 MiB memory-only volume; the Collector's file provider reads that map. No credential enters a ConfigMap, manifest, argument, log, persistent disk or Manager process. The init request/limit is 10m/16 MiB and 100m/32 MiB; the Pod's effective resource request/limit remains bounded by the Collector's larger values. Rotation requires a Collector-only rollout. Unit and pinned-image integration tests use synthetic headers only. [OTLP header format](https://opentelemetry.io/docs/specs/otel/protocol/exporter/), [Collector configuration providers](https://opentelemetry.io/docs/collector/configuration/).
