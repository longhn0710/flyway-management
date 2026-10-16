# Phase 4 homelab acceptance

Flyway Manager is running at **https://flyway-manager.apps.drgdevlab.com** in the
personal non-production homelab. Argo CD reports **Synced / Healthy**. The
[sanitized evidence record](evidence/flyway-manager-phase4-20261016.json) records
runtime, browser, Oracle, policy, restart and restore observations. No passwords,
bootstrap tokens, session cookies or key contents are included.

| Deployment identity | Accepted value |
| --- | --- |
| Application source | `d27e2542dd8e469b31112298a77b6b83cd5ce1e4` |
| Version tag | `phase4-d27e2542dd8e469b31112298a77b6b83cd5ce1e4` |
| OCI image | `ghcr.io/longhn0710/flyway-manager@sha256:9ac6accd5a30b3e53fef554f445be90922207377d521589baaf7419d95f9821f` |
| GitOps revision | `1d7d285010eace3bf54b39cddb7ce57c6e1bc124` |
| Context / namespace | `k8s-admin-public` / `flyway-manager` |
| Deployment / Service / Ingress | `flyway-manager` |
| Service | ClusterIP `10.102.100.141:8080` |
| Ingress | nginx, `103.162.31.149`, preferred hostname |
| Pod / volume | 1/1 Ready, zero container restarts; 5 GiB Bound RWO/ext4 PVC `flyway-manager-data`, Longhorn Retain, two storage replicas |

The source's [verification and publication workflow](https://github.com/longhn0710/flyway-management/actions/runs/38857697034)
passed. The image embeds the Go server, React console, Java/JDBC and Flyway
13.10.3; no runtime download is needed. All 1,458 runtime files are verified
against manifest SHA-256
`a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c`.
An isolated container also verified the pinned runtime without networking.
The acceptance harness and this report have a later delivery commit; they do
not change the deployed application binary or embedded UI.

HTTPS hostname and certificate-chain verification passed with OpenSSL return
code 0, TLS 1.3 and the existing Let's Encrypt YR1 wildcard certificate, valid
2026-09-24 through 2026-12-23. HTTP redirects with 308; HTTPS emits HSTS,
CSP, frame denial and nosniff headers. Shared DNS, wildcard certificate and
ingress-controller configuration were preserved. Cookies are Secure, HttpOnly,
SameSite=Lax. Public exposure requires application authentication and retains
ingress limits of 20 requests/second and 20 connections.

The administrator was initialized through HTTPS with protected one-time
bootstrap material. The browser verified login/logout, refresh/navigation,
registry selections, SQL upload, verified preflight, run history/details,
searchable logs and matching audit attribution. An ungranted Operator could
log in but saw no privileged navigation or authorized stream; administrative
mutations were disabled with a permission explanation. API checks independently
rejected unauthenticated requests, absent grants, invalid CSRF and wrong Origin.
Forwarded identity headers did not grant authentication; a spoofed HTTP scheme
was redirected to HTTPS. The fixed public URL controls session security.

The existing Oracle Service DNS is registered through normal APIs:
`oracle-free.flyway-manager-oracle-lab.svc.cluster.local:1521/FREEPDB1`, schema
and execution account `FWM_LAB`. Read-only info/validate/preflight succeeded
before submission; independent SQL snapshots confirmed no changes. Exactly one
V3 was then submitted through the deployed Manager after browser review:

| Version | Script | Flyway checksum | Result |
| --- | --- | --- | --- |
| V1 | `V1__isolated_lab_probe.sql` | `1105310609` | Existing row preserved |
| V2 | `V2__manager_lab_probe.sql` | `739407795` | Existing row preserved |
| V3 | `V3__manager_deployed_acceptance.sql` | `-1554549819` | Installed once, successful |

V3 asserts SESSION_USER, CURRENT_SCHEMA and PDB before inserting the isolated
probe row `(3, 'manager-phase4-v3')`. Run `mfqqwXlgjblVDa8hyJbwfU`, artifact
`R1R9iKLBMcTsF2YZtcc-hc`, actor `homelab-admin` finished **SUCCEEDED**.
The durable submission returned 202; its same-key replay returned the same run.
The five recorded commands were info, validate, **one migrate**, info, validate,
all exit 0; repair was skipped. Post-run preflight has three Success states and
no pending SQL. Downloaded record, logs, execution rows and audit agree.
Independent Oracle queries confirmed unchanged V1/V2 rows and probe markers,
exactly one V3 row, unchanged protected canary, zero cross-schema grants and
unchanged lab privileges. A direct cross-schema SELECT was denied with
ORA-00942. No clean, reset, repair or destructive failure test was performed.

Restarting the final image preserved the installation ID, the same live session,
encrypted execution credentials, registry and completed run. Read-only preflight
worked afterward and independent Oracle state was identical. The master key is
an immutable Kubernetes Secret independent of the SQLite PVC; a nonroot init
copies it into a private memory volume mounted read-only by the server.

A live SQLite snapshot was exported with the application's WAL-aware backup
command, checked with `PRAGMA integrity_check`, and stored at
`~/backups/flyway-manager/phase4-final/manager.db`. Its matching key is separately
stored at `~/backups/flyway-manager/phase4-final-keys/master.key`; directories are
0700 and files 0600. An isolated restore used the exact deployed image, no
network or published ports, and no Flyway execution configuration. Original
login, installation ID, decrypted credential startup, registry, V3 history and
all five subprocess records were restored successfully. No restored job was
submitted.

The final pod runs as UID/GID 10001, read-only root, dropped ALL capabilities,
RuntimeDefault seccomp, no privilege escalation and no service-account token.
Only SQLite/workspaces, bounded temporary storage and the private key-init
volume are writable. CPU/memory limits are 2 CPU/3 GiB. Exactly one Recreate
replica owns the filesystem; no HPA or extra database/queue was introduced.
Cilium confirms ingress and egress enforcement: DNS plus the selected Oracle
pod on 1521, with narrowly selected ingress and host-network controller access.
Unrelated API-server egress was denied. The original Oracle isolation policy
was preserved; one additive workload-specific rule permits 1521.

`make check test-race test-e2e security` passed locally and in CI: build, vet,
Go unit/integration/race tests, 11 frontend tests and 10 browser tests. Local
checks additionally exercised the real pinned runtime. Helm lint and a fully
namespaced server dry-run passed. Gitleaks and known-secret scans of repository,
application logs, image metadata and the exported image filesystem passed. npm found zero vulnerabilities;
govulncheck found zero called/imported-package vulnerabilities and one advisory
in an unused required module.

Usability review used the product specifications and existing Stitch evidence.
Many-target registration and searchable/filterable history remain available;
target/schema/account are visible during review, and run details preserve actor,
stream, artifact and result. A clear preview defect was fixed: the UI now shows
migration filenames instead of private workspace paths and accurately labels
applied and pending migrations. Blocked vendor telemetry initially delayed CLI
startup without changing Oracle; the adapter now uses the vendor's telemetry
opt-out. Request timeout diagnostics remain actionable and audited. The app-specific ingress rate was calibrated from 10 to 20 requests/second after a rapid navigation/refresh tour exhausted the original burst allowance; the same browser flow was rerun against the final settings.

Known limits: Oracle 19c and 21c were not verified; this acceptance uses Oracle
Free. No automated independent backup destination is configured; Longhorn
snapshots are not an independent backup. New Oracle destinations require a
narrow network-policy update. Historical Oracle `installed_on` values retain
their original client-zone values; rank/version/checksum identify migrations.
This is a single-instance homelab deployment, with maintenance downtime during
Recreate upgrades.

Exact status, logs, runtime, restart, backup, isolated restore, upgrade and
compatible application rollback commands are in
[homelab-operations.md](homelab-operations.md). Oracle migrations are never
rolled back by reverting an application image. The operator's protected login
file is `~/.local/share/flyway-manager/phase4/admin-credentials.json`.
