# Homelab operations

The non-production deployment is `flyway-manager` in context `k8s-admin-public`,
at `https://flyway-manager.apps.drgdevlab.com`. Kubernetes resources reconcile
through Argo CD from the `flyway-manager/` Helm chart in
`devsecopslonghn/k8s-namepsace-chart`, with its Application under `gitops/`.
The chart is the deployment source of truth; do not apply its rendered output.

The image embeds the console and the approved Flyway 13.10.3 distribution,
bundled Java and Oracle JDBC. Image acquisition occurs during CI build only.
`deploy/runtime/flyway-13.10.3.sha256` is the complete approved file manifest;
its SHA-256 is `a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c`.
The Dockerfile pins and checks the upstream archive SHA-256 as well. Vendor
notices are retained. Linux amd64 is the accepted image architecture.

## Status and runtime

```bash
kubectl --context k8s-admin-public -n flyway-manager get deployment,pod,pvc,service,ingress
kubectl --context k8s-admin-public -n flyway-manager rollout status deployment/flyway-manager --timeout=180s
kubectl --context k8s-admin-public -n flyway-manager logs deployment/flyway-manager -c manager --tail=100
kubectl --context k8s-admin-public -n argocd get application flyway-manager
curl --fail https://flyway-manager.apps.drgdevlab.com/api/v1/health
```

Log in and open Run Migration to check authenticated `/api/v1/runtime`:
`ready=true`, version `13.10.3`, integrity `verified`, bundled Java/JDBC available.
Readiness/liveness probe SQLite/server health; runtime verification remains a
separate authenticated check and pre-execution gate. No Flyway credentials or
token are needed by the probes. Forwarded user/IP/scheme headers are never
application authority: `public_url` fixes cookie security and allowed Origin;
rate limiting uses the direct peer. Only ingress/cluster-node traffic may reach
the backend, and only this workload may reach the specified Oracle lab on 1521.

## Credentials and first use

Master key source: Kubernetes Secret `flyway-manager-master-key`, independent
of the PVC. A nonroot init container copies it into a private memory volume;
the main process mounts that volume read-only. Never replace a key to resolve
a startup error. Restore the original key; another key cannot decrypt the data.
Bootstrap is one-time, requires `/data/manager/secrets/bootstrap.token`, and
does not give administrators an implicit operational grant.

The operator's private deployment material is kept under
`~/.local/share/flyway-manager/phase4/` (directory 0700; files 0600), outside Git.
Do not paste its contents into terminals, reports, issue trackers or CI.
Use the browser/API to create users, grant streams and replace Oracle
credentials. Saved Oracle passwords are write-only.

## Restart

Wait for every migration job to reach a terminal state before maintenance.
An interrupted migration is reconciled, never blindly replayed; do not assume
Oracle DDL was rolled back. The Deployment uses exactly one replica and
`Recreate`; never scale above one or add an HPA.

```bash
kubectl --context k8s-admin-public -n flyway-manager rollout restart deployment/flyway-manager
kubectl --context k8s-admin-public -n flyway-manager rollout status deployment/flyway-manager --timeout=180s
```

The restart annotation is an operational action; desired resources still come
from Git. Verify login, completed run, runtime and read-only preflight afterward.
Do not delete the PVC or SQLite WAL/SHM files.

## Backup

Choose a quiet window with no queued/running jobs and no concurrent submissions.
The `backup` CLI uses SQLite `VACUUM INTO` against a read-only source connection,
checks integrity and fsyncs the resulting snapshot. All artifacts, historical
SQL, identities, encrypted credentials, logs and audit data live in SQLite.
Do not copy a live `manager.db` without its WAL or treat storage snapshots as an
independent backup. Store data and key backups in separate protected locations.

```bash
umask 077
mkdir -p "$HOME/backups/flyway-manager/data" "$HOME/backups/flyway-manager/keys"
FWM_STAMP=$(date -u +%Y%m%dT%H%M%SZ)
kubectl --context k8s-admin-public -n flyway-manager exec deployment/flyway-manager -c manager -- \
  /usr/local/bin/flyway-manager backup --output "/tmp/manager-$FWM_STAMP.db"
kubectl --context k8s-admin-public -n flyway-manager exec deployment/flyway-manager -c manager -- \
  cat "/tmp/manager-$FWM_STAMP.db" > "$HOME/backups/flyway-manager/data/manager-$FWM_STAMP.db"
kubectl --context k8s-admin-public -n flyway-manager get secret flyway-manager-master-key \
  -o jsonpath='{.data.master\.key}' | base64 -d > "$HOME/backups/flyway-manager/keys/master-$FWM_STAMP.key"
kubectl --context k8s-admin-public -n flyway-manager exec deployment/flyway-manager -c manager -- \
  rm "/tmp/manager-$FWM_STAMP.db"
```

Record source image digest/config/backup time separately, copy both backup
locations to protected independent storage, and check transfer checksums.
The homelab's hourly Longhorn snapshots do not replace this export; discovery
found no configured independent Longhorn backup target.

## Isolated restore rehearsal

Use the exact source image digest. Create fresh disposable data/key directories,
copy the snapshot as `data/manager.db` and the original key as `keys/master.key`.
Set both directories to UID/GID 10001 with mode 0700, files 0600. Run with
`--network none`, no ports, no Flyway config and an explicit external key:

```bash
docker run -d --name flyway-manager-restore --network none --read-only \
  --cap-drop=ALL --security-opt=no-new-privileges --memory=3g --cpus=2 \
  --tmpfs /tmp:rw,nosuid,nodev,size=128m \
  -v "$FWM_RESTORE/data:/data/manager" -v "$FWM_RESTORE/keys:/run/keys:ro" \
  -e FLYWAY_MANAGER_MASTER_KEY_FILE=/run/keys/master.key \
  "$FWM_IMAGE"
docker logs flyway-manager-restore
FWM_RESTORE_PID=$(docker inspect -f '{{.State.Pid}}' flyway-manager-restore)
sudo nsenter -t "$FWM_RESTORE_PID" -n curl --fail http://127.0.0.1:8080/api/v1/health
```

Use an HTTP client inside that network namespace to log in and inspect restored
registry/history/audit. Startup verifies decryption of every saved Oracle secret.
The restore has no Oracle network route and no configured Flyway runtime.
Do not submit migration jobs in the restore. A healthy restore and original
login verify the DB/key pair; exporting only SQLite is insufficient.

## Upgrade and rollback

Publish source changes through `Verify and publish`: build/unit/race/browser/
security checks must pass before the CI token publishes a versioned image to
GHCR. Record its immutable digest, review the final GitOps chart diff, and
promote the digest through the repository workflow. Argo performs the rollout.
Check active jobs, take/export a DB+key backup, and review schema compatibility
before changing the image. Recreate ensures the old Manager stops first.

For application rollback, revert only the GitOps image digest to the previously
accepted image through the same workflow. First verify it understands the
current SQLite migration ledger; do not run an incompatible older binary or
overwrite the live database. Application rollback never rolls back Oracle
migrations. Never edit Oracle history or invoke clean/repair/reset as rollback.

The SQLite PVC uses a Longhorn RWO block filesystem with normal local locking,
not an NFS SQLite data directory. Application process locking adds a second
single-owner check. Longhorn Retain and no-prune preserve the data volume;
neither is a backup guarantee.
