# Phase 9 isolated LDAP lab proposal

These manifests are review artifacts only. Do not apply them until the user
approves the exact lab resources, Secret provisioning, Manager egress/trust
changes, Keycloak trust configuration, and acceptance realm/federation changes.
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

No live bind, TLS validation, federation login, group mapping, deprovisioning,
or OIDC acceptance has been run. See `../../docs/ldap-federation-acceptance.md`
for the proposed acceptance sequence and approval boundary.
