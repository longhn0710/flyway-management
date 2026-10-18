# Phase 9 LDAP and Keycloak federation acceptance proposal

Status: **prepared for review; no approval to provision has been received.** No
LDAP resource, Secret, credential, test user, Keycloak realm, provider, mapper,
client, or shared workload setting has been created or changed. All real bind,
federation, OIDC and deprovisioning checks remain **NOT_RUN**.

## Read-only discovery

Discovery was performed on 2026-10-18 using context `k8s-admin-public`. The
proposed namespace `flyway-manager-identity-lab` was absent. A read-only search
of all Deployments, DaemonSets, StatefulSets, Services, Ingresses and
NetworkPolicies found no LDAP/OpenLDAP resource. No LDAP-named Secret was
inspected or decoded. The existing Manager namespace is `flyway-manager`, and
the existing Keycloak namespace is `keycloak`.

Metadata-only Secret-name collision checks for the proposed LDAP TLS,
bootstrap, Keycloak vault/trust, and Manager CA names returned no matches;
Secret data was not read. A narrow all-namespace workload-name check for
`openldap`, `identity-lab`, and `ldap-acceptance` returned no matches.

The current Keycloak workload is one Ready replica, image tag
`quay.io/keycloak/keycloak:26.7.0`, at the time of inspection. Its canonical
public issuer is
`https://auth.apps.drgdevlab.com/realms/kubernetes`. Public OIDC discovery was
already verified in the Phase 9 discovery plan, but does not establish the
live realm's client, federation providers, groups, truststore/vault settings,
or administrative ownership. The live Keycloak Admin API and realm inventory
were not queried. The bootstrap ConfigMap is not treated as a complete realm
inventory. No shared realm changes are proposed.

Node requests and Longhorn metadata were read without changing the cluster:

| Node | Allocatable | Current requests | Request headroom shown by scheduler | Longhorn disk state |
| --- | --- | --- | --- | --- |
| `k8s-worker1` | 6 CPU, 15,407 MiB | 3,245m CPU, 7,866 MiB | 46% CPU, 49% memory | Ready/schedulable; 111.4 GiB available, 50 GiB scheduled |
| `k8s-worker2` | 16 CPU, 23,593 MiB | 4,260m CPU, 8,330 MiB | 74% CPU, 65% memory | Ready/schedulable; 43.9 GiB available, 50 GiB scheduled |

The proposed pod asks for 100m CPU and 256 MiB memory (limits 500m and 512 MiB)
and one 1 GiB RWO PVC on the observed `longhorn-workers` StorageClass. The
observed node and storage figures support preparing this small request; they do
not reserve capacity or guarantee scheduling at a later time. Existing
Keycloak is on `k8s-worker1`; the Manager pod selector is
`app.kubernetes.io/instance=flyway-manager,app.kubernetes.io/name=flyway-manager`.
The current Manager egress policy permits DNS and Oracle only, so an explicit
Manager-to-LDAP rule and CA mount are required. Those changes are not included
in the isolated namespace apply set and need separate authorization. Keycloak
has no ordinary NetworkPolicy in its namespace; LDAP-side ingress remains
restricted to the observed Keycloak and Manager pod selectors.

## Prepared artifacts and resource bounds

`deploy/identity-lab/` contains an isolated Namespace, ClusterIP Service,
single-replica StatefulSet, PVC template, and default-deny plus narrowly scoped
LDAPS ingress policy. The Kustomize set includes only those lab resources. A
separate proposed additive Manager egress policy is excluded from that set.
The set has five declared Kubernetes objects (Namespace, Service, StatefulSet,
and two NetworkPolicies). The StatefulSet creates one 1 GiB PVC. Two
operator-created lab Secrets are prerequisites, not checked-in objects, for
eight Kubernetes objects after an authorized setup when counting the
Namespace, declared resources, generated PVC, and Secrets.

The selected image is pinned to the current multi-architecture manifest digest
`docker.io/osixia/openldap@sha256:b23155ca4217ea928f2fec86453aabf87168a221150f26bcdcef7f3a3c84d35d`
(amd64/arm64). Registry metadata reports amd64 child digest
`sha256:f674a42d68916e9a970241bed9584e73bfaa02abe605fdd09d4452bfb3597a73`
and arm64 child digest
`sha256:c1c949e326cf388613d4e49b47299b6222aa3fc155af33b7c47948eb07b0360a`.
The pinned image configuration declares user `ldap`; its published amd64 image
filesystem was inspected read-only and maps `ldap` to UID/GID 911. This is a
pre-release v2 image, so its image maturity and acceptance suitability must be
reviewed before approval. The image is not used by the live cluster.

The lab uses `dc=flyway,dc=lab`, `ou=people`, and `ou=groups`. It has no public
Ingress, NodePort, LoadBalancer, host networking, or service-account token.
Only Service port 636 targets container port 6360. NetworkPolicy denies all
lab ingress and egress except TCP 6360 from the current Manager and Keycloak
pod selectors. The separate Manager egress proposal allows only TCP 6360 to
the LDAP pod selector. Ordinary Kubernetes NetworkPolicy uses labels; the
acceptance review must verify the cluster CNI enforces these policies and
evaluate who can create pods with matching labels.

TLS uses a dedicated operator-provisioned CA and server certificate. The
certificate SAN must match the exact service DNS name. OpenLDAP TLS is enabled
with TLS-required access rules; only LDAPS is exposed. Both clients must verify
the chain and hostname. The current Manager LDAP adapter's `CAFile` must point
to a protected read-only CA mount; the required Secret and GitOps mount are
not present. Keycloak requires the lab CA in its server truststore. Keycloak
26.7 supports `KC_TRUSTSTORE_PATHS`; adding a read-only CA mount and truststore
configuration changes the shared Keycloak workload and requires explicit
approval. Never set a trust-all or hostname-verification bypass.

Secret references in the draft:

| Namespace | Proposed Secret | Keys / use |
| --- | --- | --- |
| `flyway-manager-identity-lab` | `openldap-server-tls` | `tls.crt`, `tls.key`, `ca.crt`; certificate/key and public CA |
| `flyway-manager-identity-lab` | `openldap-bootstrap-credentials` | `data-root-password-hashed`, `config-root-password-hashed`, `readonly-password-hashed`; password hashes only |
| `keycloak` | `keycloak-ldap-acceptance-vault` | file key `flyway-ldap-acceptance_ldap-bind`; read-only bind password for the approved Keycloak file vault |
| `keycloak` | `keycloak-ldap-acceptance-ca` ConfigMap | `ca.crt`; public CA mounted read-only into the system truststore path |
| `flyway-manager` | `flyway-manager-ldap-acceptance-ca` | `ca.crt`; proposed public CA trust mount for the existing Manager pod |

All are proposals; none was found or created. The test users' passwords are
generated only after approval and supplied through the authorized directory
administration channel. No credential value or password hash is committed.
Keycloak's file vault must be enabled and verified before referencing
`${vault.ldap-bind}` in the LDAP provider. The installed server's build options,
vault configuration and Secret mount are currently unknown. If the supported
vault path is unavailable, stop for an explicit storage decision rather than
placing the bind credential in a realm export or plain provider field.

## Identity and federation contract

The existing Manager direct-LDAP adapter performs a direct user bind, then a
bounded search, and uses immutable `entryUUID` as its subject. The proposed
Manager setting uses issuer `ldaps://openldap.flyway-manager-identity-lab.svc.cluster.local:636`,
base DN `ou=people,dc=flyway,dc=lab`, bind template
`uid={username},ou=people,dc=flyway,dc=lab`, username attribute `uid`, subject
attribute `entryUUID`, display attribute `cn`, and the verified CA file. No
service-bind password is needed for Manager direct LDAP.

The isolated Keycloak realm proposal is `flyway-ldap-acceptance`, created only
after explicit approval. Proposed OIDC client: confidential web client
`flyway-manager-ldap-acceptance`, Authorization Code, PKCE S256, one exact
redirect URI `https://flyway-manager.apps.drgdevlab.com/api/v1/auth/oidc/callback`,
no wildcard redirects, no implicit/password grant, and no Admin roles derived
from token claims. The exact redirect must be checked against the running
Manager's configured public URL before any client is registered. The realm
issuer would be
`https://auth.apps.drgdevlab.com/realms/flyway-ldap-acceptance` and the
resulting OIDC subject is Keycloak's `sub`, not LDAP `entryUUID`.

Proposed LDAP federation provider settings: LDAPS URL
`ldaps://openldap.flyway-manager-identity-lab.svc.cluster.local:636`, users DN
`ou=people,dc=flyway,dc=lab`, groups DN `ou=groups,dc=flyway,dc=lab`, bind DN
`cn=readonly,dc=flyway,dc=lab`, vendor Other, username LDAP attribute `uid`,
UUID LDAP attribute `entryUUID`, RDN LDAP attribute `uid`, user object class
`inetOrgPerson`, import users enabled, edit mode READ_ONLY, and registration
disabled. Use the Keycloak provider's supported LDAP mappers for immutable UUID
and group synchronization using `groupOfNames` entries with the `member`
membership attribute. Map the named acceptance groups to a `groups` OIDC claim
for observation. The current Manager identity service
does not derive roles or grants from OIDC group claims: a user must be
explicitly provisioned in Manager by `(provider=oidc, issuer, subject)` with
locally approved role and target/schema grants. Group membership alone grants
no Manager access. Do not match on email, username, DN, or mutable display
attributes.

Direct LDAP and federated OIDC identities for the same person are separate
Manager identities because their issuer/subject pairs differ. Do not auto-link
them. Removing or disabling an LDAP entry should block the next direct bind;
Keycloak must also reject or invalidate subsequent federation authentication
according to the configured import/cache/sync behavior. Existing Manager
sessions are independent local sessions: disabling the mapped Manager user or
revoking each session is required for immediate Manager-side revocation. The
acceptance must measure cache/session behavior and prove local Admin recovery
remains available throughout.

## Real acceptance sequence (NOT_RUN)

After an explicit approval of the exact reviewed files and provisioning steps:

1. Provision only the listed namespace-scoped resources and required Secrets.
   Confirm no Ingress, NodePort, LoadBalancer, unexpected pod, or broad egress.
2. From the approved Manager and Keycloak workloads, verify valid TLS chain and
   exact hostname, TLS version, successful read-only search/bind, and absence
   of a plaintext LDAP path. Confirm an unapproved-label pod cannot connect.
3. Create disposable Viewer, Operator, and Admin-intended LDAP users and groups
   through a separately approved operator procedure. Do not grant Manager
   Admin by group claim. Provision exact `(issuer, sub)` users with least
   privilege through Manager's local Admin path.
4. Test correct/wrong passwords, unknown/inactive users, ambiguous search,
   escaped username/DN/filter input, certificate hostname/CA errors, and
   directory outage. Verify local Admin login and service-token behavior.
5. Perform a real browser OIDC Authorization Code + PKCE login through the
   isolated realm. Inspect the signed token claims without retaining tokens;
   verify issuer, audience, subject, group claim and exact callback.
6. Remove a user from each named group, disable/delete a user in LDAP, run the
   approved Keycloak sync/relogin flow, and test both new login and existing
   Manager sessions. Record session expiry/revocation requirements explicitly.
7. Verify Viewer/Operator/Admin and target/schema/PROD authorization, absence
   of email-based linking or escalation, and preservation of the local Admin
   account. Keep audit evidence while disabling test users and revoking test
   tokens/sessions at cleanup.

No mock-only protocol result satisfies this gate. Evidence must include actual
TLS/bind results, federation provider configuration with secrets redacted,
browser OIDC result, explicit RBAC checks, denial from an unauthorized pod,
and cleanup state. Do not label the lab accepted until these checks pass.

## Approval boundary

Step 4 of the goal requires approval before **any** new LDAP lab provisioning or
shared realm changes. The exact concrete review set is:

- the Namespace, StatefulSet, Service, PVC template and NetworkPolicies in
  `deploy/identity-lab/`;
- the image choice (including its pre-release status), all CPU/memory/storage
  bounds, TLS and credential Secret names/keys, and operator-only Secret
  creation procedures;
- the Manager egress policy, CA Secret mount and direct-LDAP settings;
- the Keycloak truststore mount/configuration, file-vault availability and
  Secret reference, new realm/client, LDAP provider and mapper configuration;
- the disposable test-user/group creation and deprovisioning protocol.

This task stops at that approval boundary. No deployment command has been
executed.

## Primary documentation references

- [OpenLDAP v2 container documentation](https://github.com/osixia/container-openldap/blob/develop/README.md) and [registry tag/digest metadata](https://hub.docker.com/r/osixia/openldap/tags): listener ports, bootstrap variables, TLS-required option and current image digest. The pinned tag is a beta proposed only for disposable acceptance.
- [Keycloak 26.7 LDAP federation guide](https://www.keycloak.org/docs/26.7.0/server_admin/index.html): LDAPS trust, LDAP import/sync, federation, and mapper behavior.
- [Keycloak truststore guide](https://www.keycloak.org/server/keycloak-truststore): `KC_TRUSTSTORE_PATHS` and strict hostname handling for LDAP.
- [Keycloak vault guide](https://www.keycloak.org/server/vault): file-vault support for LDAP bind credentials, realm-prefixed Secret filenames, and build-time enablement.
- [Kubernetes NetworkPolicy API](https://kubernetes.io/docs/reference/kubernetes-api/networking/network-policy-v1/): namespace and pod selector semantics used by the proposed ingress rules.
