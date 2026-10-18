# Phase 9 isolated LDAP lab proposal

The user approved this isolated lab and it was provisioned on 2026-10-18.
Existing Manager egress/trust and shared Keycloak realm/provider changes remain
separately gated; approval of the lab does not authorize those changes.
The `manager-egress-proposal.yaml` file changes the existing application
namespace and is intentionally outside the isolated Kustomize set.

Prerequisite Secrets, whose values must be generated and provisioned by the
authorized operator after approval:

- Namespace `flyway-manager-identity-lab`, Secret `openldap-server-tls`, keys
  `tls.crt`, `tls.key`, and `ca.crt`. The certificate SAN must include
  `openldap.flyway-manager-identity-lab.svc.cluster.local`.
- Namespace `flyway-manager-identity-lab`, Secret
  `openldap-bootstrap-credentials`, keys `data-root-password-hashed`,
  `config-root-password-hashed`, and `readonly-password-hashed`. Values are
  OpenLDAP password hashes, never plaintext credentials. The pinned image's
  bootstrap script reads the three Secret-backed `*_PASSWORD_HASHED` variables
  during first initialization.

No Secret object or credential value is included here. The Keycloak federation
bind account is proposed as `cn=readonly,dc=flyway,dc=lab`; after the realm is
approved, its password should be delivered through the Keycloak file-vault
integration, not embedded in realm JSON, command arguments, or this repository.
That integration is not yet verified in the installed Keycloak deployment.

The lab service exposes only LDAPS on ClusterIP port 636. The pinned image is
configured for LDAPS plus its local LDAPI bootstrap socket; its plaintext LDAP
network listener is disabled. A nonroot init container creates the persistent
`config`, `openldap-data`, `openldap-backups`, and `run` directories before the
StatefulSet mounts the volume with `subPath`. The image copies bundled modules
and schemas into image filesystem paths at startup, so the proposal keeps the
container root filesystem writable while mounting `/run` and `/tmp` as bounded
memory-backed volumes.
The namespace applies Pod Security `restricted`, no service-account token,
nonroot UID/GID 911, dropped capabilities, seccomp RuntimeDefault, bounded CPU
and memory, and one 1 GiB Longhorn RWO volume.

Real direct bind, native Manager LDAP and TLS/network probes have run. Federation
and deprovisioning results, failures and qualifications are maintained in
[the acceptance report](../../docs/ldap-federation-acceptance.md). The generated
ACL ConfigMap contains only public rules, including self entryUUID read and
password-hash denial. Never put a password/hash in this ConfigMap. The current
StatefulSet uses worker2, persistent config/data and a mkdir-only init; it never
resets an existing directory. Hash files used by vendor bootstrap must have no
terminal newline (LDIF substitution is literal).
