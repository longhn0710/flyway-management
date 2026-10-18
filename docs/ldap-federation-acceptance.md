# Phase 9 LDAP and Keycloak federation acceptance

Status on 2026-10-18: **PASS for the isolated real-directory/local-Keycloak profile; shared deployment remains gated**. The user explicitly approved the concrete one-pod lab proposal. No shared Keycloak realm/client, Manager authentication, CA mount, existing grants or execution settings changed. No SQL was executed. The original checkout and Phase 8 installation remain preserved.

## Discovery and approved resource boundary

Read-only discovery used `k8s-admin-public`. The namespace and LDAP resource/Secret names were absent before provisioning. Existing Manager namespace is `flyway-manager`; shared Keycloak namespace is `keycloak`, issuer `https://auth.apps.drgdevlab.com/realms/kubernetes`. The shared realm Admin API was not queried or changed. Initial observed capacity supported this disposable lab:

| Node | Allocatable | Current requests | Request headroom shown by scheduler | Longhorn disk state |
| --- | --- | --- | --- | --- |
| `k8s-worker1` | 6 CPU, 15,407 MiB | 3,245m CPU, 7,866 MiB | 46% CPU, 49% memory | Ready/schedulable; 111.4 GiB available, 50 GiB scheduled |
| `k8s-worker2` | 16 CPU, 23,593 MiB | 4,260m CPU, 8,330 MiB | 74% CPU, 65% memory | Ready/schedulable; 43.9 GiB available, 50 GiB scheduled |


The approved setup is one nonroot OpenLDAP Pod in `flyway-manager-identity-lab`, one internal ClusterIP LDAPS Service, one 1 GiB Longhorn RWO PVC, default-deny policies and two operator-provisioned lab Secrets. Request 100m CPU/256 MiB; limits 500m/512 MiB. Namespace PSA is restricted; UID/GID 911, RuntimeDefault seccomp, dropped capabilities, no service-account token. `/run` and `/tmp` use bounded memory volumes. The approved writable root filesystem supports the vendor's module/schema bootstrap copies. No public Ingress, NodePort, LoadBalancer, host networking or broad egress was created.

The pinned beta image is `docker.io/osixia/openldap@sha256:b23155ca4217ea928f2fec86453aabf87168a221150f26bcdcef7f3a3c84d35d`, actual OpenLDAP 2.6.15-beta. This choice and its beta/writable-root limitations were included in approval. It is qualified only as a disposable identity fixture, not a production directory. The tested amd64 child is `sha256:f674a42d68916e9a970241bed9584e73bfaa02abe605fdd09d4452bfb3597a73`.

The current Kustomize set declares six objects: Namespace, generated non-secret ACL ConfigMap, Service, StatefulSet and two NetworkPolicies. The StatefulSet adds PVC `data-openldap-0`; the operator supplies two Secrets, giving nine objects when counting these prerequisites. The narrow ACL ConfigMap grants users read access to their own immutable `entryUUID` while preserving password-hash denial. It contains no credential. A vendor seed path is redirected to persistent `/var/lib/openldap/config`; data/config/backups remain on the same PVC. The mkdir-only init does not reset existing data. Scheduling is explicitly on `k8s-worker2`, where the Longhorn CSI is available.

The Service `openldap` exposes 636→6360. Only LDAPS and local LDAPI are enabled; `/proc/net/tcp` showed TCP 6360 as the only listener. Namespace policies deny all other ingress/egress and permit LDAPS only from the observed Manager/Keycloak namespace+Pod selectors. No Manager LDAP egress proposal was applied. The existing Manager egress rules consequently still prevent normal LDAP authentication until separately authorized.

## TLS, credentials and observed security checks

| Verified lab Secret | Keys / use |
| --- | --- |
| `openldap-server-tls` | `tls.crt`, `tls.key`, `ca.crt`; dedicated server identity |
| `openldap-bootstrap-credentials` | `data-root-password-hashed`, `config-root-password-hashed`, `readonly-password-hashed`; hashes only |

Original operator materials are protected under `/home/longhn0710/.local/share/flyway-manager/phase9-ldap-20261018`, directory 0700/files 0600. No Secret API value was read, decoded, printed or committed. No password or hash appears in manifests/evidence. The server certificate expires 2027-01-16 01:01:32 UTC, with exact Service DNS and loopback IP SANs. The loopback SAN supports the local acceptance forward; it is not a hostname-verification bypass. Secret encryption at rest is not established.

A real verified LDAPS bind/search using the original read-only account returned the three fixture users and groupOfNames mappings. TLS 1.3 and the exact Service hostname/CA were verified. Actual Manager `AuthenticateLDAP` calls succeeded for Admin/Operator/Viewer fixtures using `entryUUID`, and rejected wrong password, escaped filter input, untrusted CA, wrong hostname and ambiguous search. A read-only account write attempt was denied; a bounded userPassword lookup returned no credential attribute. These role names are fixture names; Manager authority requires independent local mapping.

A temporary [`LdapTLSProbe.java`](../scripts/identity/LdapTLSProbe.java) TLS probe from the **existing Keycloak Pod** verified the exact LDAP Service hostname and dedicated CA over TLS 1.3. It loaded only a public CA into its own temporary SSLContext; no JVM/global trust or realm setting changed, no bind credential was supplied, and its temporary files were removed. The excluded `network-probe-proposal.yaml` records the exact temporary probe; its fixed Service IP must be reverified before reuse. An unapproved-label Pod in the lab timed out connecting to Service TCP 636; it was then deleted. This proves that probe's denial, not resistance to namespace administrators who can create approved labels. Local port-forward federation tests bypass workload NetworkPolicy and are reported separately.

[Runtime evidence](evidence/ldap-federation-20261018/lab-runtime.json) binds the Ready Pod/image/PVC and these security observations. The LDAP fixture user UUIDs remain stable across the lab rollout. The read-only account may read directory identity/group metadata; ordinary users receive only the narrow self UUID permission needed by the existing direct-LDAP adapter.

## Bootstrap and harness failures retained

Initial scheduling selected a node without the required Longhorn CSI; the existing PVC was retained and the workload constrained to worker2. The pinned image bootstrap then failed because terminal newlines in the protected hash files created an invalid LDIF blank line. A local pinned-image reproduction established the cause; only terminal newlines in the original hash encoding were removed before provisioning the same hashes. Credentials were not rotated, and incomplete first-start config directories remain archived on the lab PVC. No directory database was reset.

The first successful bind exposed the vendor default ACL's omission of self `entryUUID` reads. A lab-only config-root update added the narrow rule, now also represented in the bootstrap ConfigMap. No shared identity/provider authorization was bypassed. Earlier local federation attempts failed on connection resets/empty user/group results through an operator port-forward. Those attempts are not clean passes. Temporary disposable users retained by a failed forwarding cleanup were inventoried and removed by exact generated UID after membership inspection; original fixture users/groups were preserved. One later attempt reached real signed claims but its local Keycloak admin token expired during the normal Manager rate-limit waits; the harness now refreshes that token before administrative calls without retrying ambiguous mutations or changing product limits. The final complete case passed.

## Real local federation protocol

`web/tests/ldap-live.spec.ts` is an opt-in acceptance harness using this **real cluster directory** and a disposable local Keycloak 26.7.0 pinned to `quay.io/keycloak/keycloak@sha256:0f198be292568439d700cdbfb893e69a6009bb43a94a06a945b1d3d506c76b13`. It creates only a uniquely named Docker container, loopback HTTPS endpoint, private realm/client and temporary local Manager registry/key. Shared Keycloak remains untouched. Local fixture files are 0600 inside 0700 directories and removed with the uniquely named container. No JWT, authorization code/verifier, password, credential or full provider log is retained. Failure diagnostics expose only exception class names/fixed transport flags.

The real directory uses `dc=flyway,dc=lab`, users under `ou=people`, groups under `ou=groups`, and `groupOfNames/member`. Federation is READ_ONLY, import enabled, cache NO_CACHE, read-only bind, username/RDN `uid`, immutable UUID `entryUUID`, no registration or write-back. The fixture trusts the dedicated LDAP CA explicitly. A separate loopback diagnostic Code+PKCE client verifies signed group claims in memory; Manager does not derive roles/grants from those claims.

Manager identities are explicitly provisioned by `(provider, issuer, subject)` with local Admin/Operator/Viewer roles. Direct LDAP uses `entryUUID`; federated OIDC uses Keycloak `sub`. The fixture verifies they remain separate identities. No username/email autolinking or group-derived Admin grant is allowed. The original local Admin path remains enabled. An inert PROD target has port 1 and no SQL; grants/service metadata and false PROD bits must remain unchanged.

The disposable deprovision case removes one generated `phase9-retire-*` member, syncs groups, deletes that exact directory entry, and tests new direct/federated login denial. Existing Manager sessions are independent: directory removal alone is **not** immediate Manager session revocation; explicit local user disable must revoke the session. The current product also revokes only its local session on logout; upstream RP logout is not implemented. Cache/sync behavior and revocation limits must remain explicit.

Run only against the authorized directory and protected original material, after starting `python3 scripts/identity/forward-ldap.py` in a separate terminal. That loopback byte relay uses one bounded exec stream per connection into the verified Ready lab Pod; it never terminates TLS or reads payloads. This avoids one failing shared port-forward interrupting unrelated LDAP connections:

```sh
make build
FWM_LDAP_LIVE=1 \
FWM_LDAP_OPERATOR_DIRECTORY=/protected/original/ldap-directory \
FWM_LDAP_EVIDENCE=/protected/new/evidence-directory \
  npm --prefix web run test:ldap
```

The ordinary CI suite skips the live-directory case and requires no cluster credentials. `scripts/identity/ldap-lab` accepts only the approved LDAPS endpoints, verifies CA/hostname, bounds queries/timeouts, and restricts mutations to generated disposable UIDs. Passwords come from protected files, never command-line values; LDAP PasswordModify hashes the disposable password on the server. Read-only inventory must precede cleanup after an ambiguous failure; do not blindly retry mutations.

## Remaining acceptance and rollout gates

The full real-directory browser test passed on 2026-10-18 in 3.4 minutes. [Sanitized federation evidence](evidence/ldap-federation-20261018/ldap-federation.json) records three synchronized groups/four users with zero sync failures, the explicit Admin/Operator/Viewer mappings, inactive-user denial, separate native identity and a verified signed group claim. Viewer access to Admin settings was denied. The generated user's group removal became visible after sync; directory deletion denied new native/federated login with `prompt=login`. Cached upstream SSO-session revocation was not tested or claimed; immediate Manager denial depends on explicit local disable. Its existing Manager session remained valid until explicit Manager-user disable, which then returned 401. User/service scopes and false PROD bits remained identical. This is not automatic directory-to-Manager grant revocation or provider-wide logout.

The test stopped only its own local provider and authenticated the original local fixture Admin. The [post-fallback screenshot](evidence/ldap-federation-20261018/local-admin-after-federation.png) was visually inspected. Temporary Docker/Manager/realm/client/session files were removed; the [final directory inventory](evidence/ldap-federation-20261018/final-directory.json) contains the original three users/groups and no disposable retire UID. The approved lab/PVC/keys remain retained for review.

`make check test-race test-e2e security` passed: full Go vet/unit/race suites, 26 frontend tests, 42 browser passes and two opt-in skips (PostgreSQL and this real LDAP case). The LDAP case was then run separately against the actual directory and passed. After the focused harness refresh change, frontend build/lint and the complete live case passed again; Go helper vet, real read-only credential-attribute denial, Python syntax, JSON/local-link validation and known protected credential/key scans also passed. No affecting Go vulnerability was found (one uncalled module advisory); npm audit found zero vulnerabilities. Ordinary CI does not receive cluster credentials or run the lab case; [PR #9](https://github.com/longhn0710/flyway-management/pull/9) retains the focused change; current-head PR CI remains a separate gate.

Existing Manager authentication settings, client registration, trust mounts and LDAP egress need a separately reviewed deployment authorization. The excluded `manager-egress-proposal.yaml` and any shared Keycloak trust/vault resources are proposals only; they are not part of this lab apply. A future shared provider must use an approved credential-storage path (for example supported Keycloak file vault), exact callback, verified CA and preprovisioned subjects; the fixture's ephemeral provider state is not a shared-realm rollout recipe. Preserve local Admin, all existing user/service grants, PROD controls, migration history and execution safety. Do not merge, deploy the main Manager or edit the shared realm automatically.

## Primary documentation references

- [OpenLDAP v2 container documentation](https://github.com/osixia/container-openldap/blob/develop/README.md) and [registry tag/digest metadata](https://hub.docker.com/r/osixia/openldap/tags): listener ports, bootstrap variables, TLS-required option and current image digest. The pinned tag is a beta proposed only for disposable acceptance. Its pinned amd64 filesystem and startup scripts were extracted locally without starting a container to verify UID 911, persistent paths, listener defaults, module/schema writes, and Secret-backed bootstrap environment names.
- [vegardit OpenLDAP image documentation](https://github.com/vegardit/docker-openldap/blob/main/README.md) and [pinned startup script source](https://github.com/vegardit/docker-openldap/blob/8331dc4/image/run.sh): current supported volumes, secret-file inputs, TLS settings, and its root-level UID/GID and ownership operations. Its image was inspected but never started.
- [Keycloak 26.7 LDAP federation guide](https://www.keycloak.org/docs/26.7.0/server_admin/index.html): LDAPS trust, LDAP import/sync, federation, and mapper behavior.
- [Keycloak truststore guide](https://www.keycloak.org/server/keycloak-truststore): `KC_TRUSTSTORE_PATHS` and strict hostname handling for LDAP.
- [Keycloak vault guide](https://www.keycloak.org/server/vault): file-vault support for LDAP bind credentials, realm-prefixed Secret filenames, and build-time enablement.
- [Kubernetes NetworkPolicy API](https://kubernetes.io/docs/reference/kubernetes-api/networking/network-policy-v1/): namespace and pod selector semantics used by the proposed ingress rules.


## Supplementary failure regressions

The completion audit added direct adapter regressions for an explicitly trusted but expired certificate under both LDAPS and StartTLS, plus an outage after a successful bind. `TestLDAPRejectsExpiredTrustedCertificate` returns a sanitized failure/empty identity for both transports; `TestLDAPDirectoryOutageDoesNotReuseAuthenticatedIdentity` stops the previously working listener and proves no cached identity is returned. All LDAP tests and the full adapter race suite passed. These are private TLS/protocol fixtures, supplementing the retained real-directory federation proof; they do not claim a cluster certificate replacement, directory shutdown or shared-deployment acceptance. Production provider and execution code are unchanged.
