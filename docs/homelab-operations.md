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

Check authenticated runtime readiness without printing session material:

```bash
python3 - <<'PYTHON'
import http.cookiejar, json, pathlib, urllib.request
base = 'https://flyway-manager.apps.drgdevlab.com'
c = json.loads((pathlib.Path.home()/'.local/share/flyway-manager/phase4/admin-credentials.json').read_text())
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
request = urllib.request.Request(base+'/api/v1/auth/login', data=json.dumps(dict(provider='local', username=c['username'], password=c['password'])).encode(), headers={'Origin':base, 'Content-Type':'application/json'})
with client.open(request, timeout=30) as response:
    assert response.status == 200
with client.open(base+'/api/v1/runtime', timeout=30) as response:
    runtime = json.load(response)['data']
print(json.dumps({key: runtime[key] for key in ('ready','version','integrity','dependency','blockers')}))
PYTHON
```

The Run Migration screen shows the same authenticated `/api/v1/runtime`:
`ready=true`, version `13.10.3`, integrity `verified`, bundled Java/JDBC available.
Readiness/liveness probe SQLite/server health; runtime verification remains a
separate authenticated check and pre-execution gate. No Flyway credentials or
token are needed by the probes. Forwarded user/IP/scheme headers are never
application authority: `public_url` fixes cookie security and allowed Origin;
rate limiting uses the direct peer. Only ingress/cluster-node traffic may reach
the backend, and only this workload may reach the specified Oracle lab on 1521.

The adapter always sets `REDGATE_DISABLE_TELEMETRY=true`, the
[vendor-supported usage telemetry opt-out](https://documentation.red-gate.com/fd/redgate-disable-telemetry-environment-variable-277579301.html).
This avoids blocked external telemetry connections delaying each CLI startup in
the isolated pod; full manifest/version checks and migration policy still run.

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
  /usr/local/bin/flyway-manager backup --output "/data/manager/backup-$FWM_STAMP.db"
kubectl --context k8s-admin-public -n flyway-manager exec deployment/flyway-manager -c manager -- \
  cat "/data/manager/backup-$FWM_STAMP.db" > "$HOME/backups/flyway-manager/data/manager-$FWM_STAMP.db"
kubectl --context k8s-admin-public -n flyway-manager get secret flyway-manager-master-key \
  -o jsonpath='{.data.master\.key}' | base64 -d > "$HOME/backups/flyway-manager/keys/master-$FWM_STAMP.key"
kubectl --context k8s-admin-public -n flyway-manager exec deployment/flyway-manager -c manager -- \
  rm "/data/manager/backup-$FWM_STAMP.db"
```

Record source image digest/config/backup time separately, copy both backup
locations to protected independent storage, and check transfer checksums.
The homelab's hourly Longhorn snapshots do not replace this export; discovery
found no configured independent Longhorn backup target.

## Isolated restore rehearsal

The accepted Phase 4 image is:

```bash
FWM_IMAGE=ghcr.io/longhn0710/flyway-manager@sha256:9ac6accd5a30b3e53fef554f445be90922207377d521589baaf7419d95f9821f
FWM_RESTORE="$HOME/flyway-manager-restore-rehearsal"
install -d -m 700 "$FWM_RESTORE/data" "$FWM_RESTORE/keys"
sudo install -o 10001 -g 10001 -m 600 "$HOME/backups/flyway-manager/phase4-final/manager.db" "$FWM_RESTORE/data/manager.db"
sudo install -o 10001 -g 10001 -m 600 "$HOME/backups/flyway-manager/phase4-final-keys/master.key" "$FWM_RESTORE/keys/master.key"
sudo chown 10001:10001 "$FWM_RESTORE/data" "$FWM_RESTORE/keys"
```

Use the exact source image digest. Create fresh disposable data/key directories,
copy the snapshot as `data/manager.db` and the original key as `keys/master.key`.
Set both directories to UID/GID 10001 with mode 0700, files 0600. Run with
`--network none`, no ports, no Flyway config and an explicit external key:

```bash
docker run -d --name flyway-manager-restore-rehearsal --network none --read-only \
  --cap-drop=ALL --security-opt=no-new-privileges --memory=3g --cpus=2 \
  --tmpfs /tmp:rw,nosuid,nodev,size=128m \
  -v "$FWM_RESTORE/data:/data/manager" -v "$FWM_RESTORE/keys:/run/keys:ro" \
  -e FLYWAY_MANAGER_MASTER_KEY_FILE=/run/keys/master.key \
  "$FWM_IMAGE"
docker logs flyway-manager-restore-rehearsal
FWM_RESTORE_PID=$(docker inspect -f '{{.State.Pid}}' flyway-manager-restore-rehearsal)
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

For a reviewed image upgrade, edit only `image.digest` in the isolated GitOps
checkout, then validate and publish the focused change:

```bash
cd /home/longhn0710/workspace/flyway-manager-gitops
# Set image.digest in flyway-manager/values.yaml to the reviewed published digest.
helm lint flyway-manager
helm template flyway-manager flyway-manager --namespace flyway-manager > /tmp/flyway-manager-reviewed.yaml
git diff -- flyway-manager/values.yaml
git add flyway-manager/values.yaml
git commit -m "deploy(flyway-manager): promote reviewed image"
git push origin HEAD:master
kubectl --context k8s-admin-public -n argocd get application flyway-manager
kubectl --context k8s-admin-public -n flyway-manager rollout status deployment/flyway-manager --timeout=240s
```

Confirm the Deployment's image equals the new digest before trusting rollout
status; an old revision can still be healthy while Argo fetches Git. The
previous accepted compatible digest is
`sha256:8437685899bd0154a9964268db51487b7394e47d5a515ad20ee54f88236a813e`.

For application rollback, revert only the GitOps image digest to the previously
accepted image through the same workflow. First verify it understands the
current SQLite migration ledger; do not run an incompatible older binary or
overwrite the live database. Application rollback never rolls back Oracle
migrations. Never edit Oracle history or invoke clean/repair/reset as rollback.

The SQLite PVC uses a Longhorn RWO block filesystem with normal local locking,
not an NFS SQLite data directory. Application process locking adds a second
single-owner check. Longhorn Retain and no-prune preserve the data volume;
neither is a backup guarantee.

To return specifically to the previous accepted image while preserving the
later ingress calibration, review the inverse of the image-only promotion:

```bash
cd /home/longhn0710/workspace/flyway-manager-gitops
git show c5e503b4756051e3c49864b5caf2bef216dd3d08 -- flyway-manager/values.yaml
git revert --no-edit c5e503b4756051e3c49864b5caf2bef216dd3d08
git push origin HEAD:master
```

Use this only after the quiet-window backup and compatibility review described
above; it changes the application image and leaves Oracle history intact.
