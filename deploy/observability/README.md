# Scoped observability deployment proposal

**Do not apply without explicit approval.** No resources in this directory have been deployed. Review with [feature contract and acceptance](../../docs/elastic-cloud-integration.md).

`kubectl kustomize deploy/observability` renders one ConfigMap, one Deployment, one ClusterIP Service, two namespaced NetworkPolicies and one CiliumNetworkPolicy in existing namespace `flyway-manager`. Deployment request 50m/64 MiB, limit 200m/256 MiB, one replica; nonroot UID/GID 10001, read-only root, no privilege escalation/capabilities/service-account token, no PVC/hostPath/cluster permissions. Cilium DNS and FQDN rules allow only the exact supplied Elastic endpoint on HTTPS. Kubernetes additive Manager egress allows only the internal collector on 4318. Existing Oracle/PostgreSQL/DNS rules remain in place. Validate live Cilium policy composition and cluster capacity before approval.

The collector exposes only mTLS OTLP HTTP 4318 and has no public ingress. Its private server identity requires SAN `flyway-manager-observability.flyway-manager.svc.cluster.local`, serverAuth EKU, and a dedicated CA. Only the approved Manager client identity should be issued clientAuth by that CA. Keep the CA private key in protected operator storage, not cluster/Git/evidence. TLS Secrets are proposed, not verified:

- `flyway-manager-observability-tls`: collector `tls.crt`, `tls.key`, `ca.crt` (client trust).
- `flyway-manager-observability-client-tls`: Manager `tls.crt`, `tls.key`, `ca.crt` (server trust).
- Existing verified reference `flyway-manager-elastic-api-key/api-key`: collector only, never application/init/Flyway containers. Its value is not validated by metadata checks.

Operator provisions TLS materials in a private 0700 directory with 0600 files, validates chain/SAN/EKUs/expiry and creates Secrets using `kubectl create secret generic NAME --from-file=...` with file **paths**, never key/password values in arguments. Do not print generated manifests. Review rotation before touching an existing Secret; no overwrite commands are supplied here. Secret encryption at rest is not established.

`manager-config-proposal.json` is a disabled configuration fragment to merge into existing config; replacing the entire live config would be unsafe. Enable only after authorized rollout. `manager-trust-proposal.yaml` is a separate strategic merge sketch for the existing `manager` container and Secret mount. Both are **excluded** from the collector kustomization; adapt/render via established GitOps chart and verify the exact diff before authorization. Application deployment/rollback retains original data/PVC/master key and unchanged SQLite schema.

The collector configuration has no credentials: its environment expands only the Secret-sourced Elastic key at runtime. Use no debug exporter or payload/HTTP-header logging. Its receiver authenticates dedicated client certs and drops foreign service/environment/version records; its independent field allowlist removes arbitrary bodies/attributes. It cannot authorize individual user claims and does not mount the registry.

Before any apply, record the rendered object inventory, immutable image/config hash, Secret metadata, Cilium enforcement and resource capacity. Acceptance must include actual indexed search and dashboard evidence. A collector-only readiness result cannot authorize the existing Manager configuration change or establish ingestion acceptance.
