#!/usr/bin/env bash
# Controlled direct-Flyway PostgreSQL Gate E/F fixture runner.
# Deliberately refuses execution until the caller records that Gates A-D passed.
set -euo pipefail
umask 077

if [[ "${1:-}" != --approved-after-gates-a-d ]]; then
  cat >&2 <<'EOF'
Gate E/F is prepared; Gates A-D confirmation is required.
After independent Gates A-D pass, invoke with:
  scripts/postgres-lab/gate-ef.sh --approved-after-gates-a-d
This creates a new disposable database named fwm_gate_acceptance and retains
private logs/configuration under the lab state directory.
EOF
  exit 2
fi

STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/flyway-manager-postgres-lab"
CONTAINER="flyway-manager-postgres17-lab"
RUNTIME="${FLYWAY_13_10_3_HOME:-/tmp/flyway-oracle-acceptance-20261016/source/flyway-13.10.3}"
MANIFEST_SHA256='a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c'
RUN_ID=$(date -u +%Y%m%dt%H%M%Sz)
DATABASE="fwm_gate_acceptance_${RUN_ID}"
export FWM_GATE_DATABASE="$DATABASE"
EVIDENCE="$STATE_DIR/gate-ef-$RUN_ID-evidence"
FIXTURES="$STATE_DIR/gate-ef-$RUN_ID-fixtures"
CONFIG="$STATE_DIR/gate-ef-$RUN_ID.toml"

[[ -r "$STATE_DIR/lab.env" ]] || { echo 'PostgreSQL lab credentials are unavailable.' >&2; exit 1; }
[[ -x "$RUNTIME/flyway" && -r "$RUNTIME/flyway-manifest.sha256" ]] || { echo 'Pinned Flyway 13.10.3 distribution was not found.' >&2; exit 1; }
actual_manifest=$(sha256sum "$RUNTIME/flyway-manifest.sha256" | cut -d ' ' -f 1)
[[ "$actual_manifest" == "$MANIFEST_SHA256" ]] || { echo 'Pinned Flyway manifest identity mismatch.' >&2; exit 1; }
if ! (cd "$RUNTIME" && sha256sum -c flyway-manifest.sha256 >/dev/null 2>&1); then
  echo 'Pinned Flyway distribution failed manifest verification.' >&2
  exit 1
fi
if docker inspect "$CONTAINER" >/dev/null 2>&1; then
  :
else
  echo 'PostgreSQL lab container is not running.' >&2
  exit 1
fi
set -a
source "$STATE_DIR/lab.env"
set +a
[[ ! -e "$EVIDENCE" && ! -e "$FIXTURES" && ! -e "$CONFIG" ]] || {
  echo 'Gate E/F artifacts already exist; preserve them and choose a fresh lab state directory.' >&2
  exit 1
}
mkdir -m 700 "$EVIDENCE" "$FIXTURES"
mkdir -p "$STATE_DIR/gate-ef-home"
chmod 700 "$STATE_DIR/gate-ef-home"
chmod 600 "$STATE_DIR/postgres_password"
IFS= read -r LAB_ADMIN_PASSWORD < "$STATE_DIR/postgres_password"
trap 'unset LAB_ADMIN_PASSWORD PGPASSWORD PGREADONLY_PASSWORD' EXIT

if docker exec -e PGPASSWORD="$LAB_ADMIN_PASSWORD" "$CONTAINER" psql -XAtq \
  -U lab_admin -d postgres -c "SELECT 1 FROM pg_database WHERE datname='$DATABASE'" | rg -q '^1$'; then
  echo 'The dedicated Gate E/F database already exists; refusing to overwrite it.' >&2
  exit 1
fi
docker exec -e PGPASSWORD="$LAB_ADMIN_PASSWORD" "$CONTAINER" createdb -U lab_admin "$DATABASE" >/dev/null
docker exec -e PGPASSWORD="$LAB_ADMIN_PASSWORD" "$CONTAINER" psql -v ON_ERROR_STOP=1 \
  -U lab_admin -d "$DATABASE" -c "GRANT CONNECT ON DATABASE $DATABASE TO fwm_exec;" >/dev/null

for schema in gate_success gate_rollback gate_partial gate_checksum gate_isolation gate_future gate_concurrent gate_timeout; do
  docker exec -e PGPASSWORD="$LAB_ADMIN_PASSWORD" "$CONTAINER" psql -v ON_ERROR_STOP=1 \
    -U lab_admin -d "$DATABASE" -c "CREATE SCHEMA $schema AUTHORIZATION fwm_exec;" >/dev/null
done
docker exec -i -e PGPASSWORD="$LAB_ADMIN_PASSWORD" "$CONTAINER" psql -v ON_ERROR_STOP=1 \
  -U lab_admin -d "$DATABASE" <<'SQL' >/dev/null
CREATE SCHEMA gate_other AUTHORIZATION lab_admin;
CREATE TABLE gate_other.guard(id integer PRIMARY KEY, note text NOT NULL);
INSERT INTO gate_other.guard VALUES (1, 'unchanged');
REVOKE ALL ON SCHEMA gate_other FROM PUBLIC;
REVOKE ALL ON TABLE gate_other.guard FROM PUBLIC;
SQL

# The password is generated hexadecimal; json.dumps produces TOML-compatible
# basic strings. URL-encode the CA path so spaces in a custom state directory
# cannot alter JDBC properties.
python3 - "$CONFIG" <<'PY'
import json, os, sys, urllib.parse
path, = sys.argv[1:]
host, port = os.environ["PGHOST"], os.environ["PGPORT"]
database, username, password = (os.environ[k] for k in ("PGDATABASE", "PGUSER", "PGPASSWORD"))
ca = urllib.parse.quote(os.environ["PGSSLROOTCERT"], safe="/")
url = f"jdbc:postgresql://{host}:{port}/{os.environ['FWM_GATE_DATABASE']}?sslmode=verify-full&sslrootcert={ca}"
with open(path, "w", encoding="utf-8") as f:
    f.write("[environments.gate]\n")
    f.write("url = " + json.dumps(url) + "\n")
    f.write("user = " + json.dumps(username) + "\n")
    f.write("password = " + json.dumps(password) + "\n")
    f.write("[flyway]\n")
    f.write('environment = "gate"\n')
    f.write('defaultSchema = "gate_success"\n')
    f.write('table = "flyway_schema_history"\n')
    f.write("createSchemas = false\ncleanDisabled = true\nvalidateOnMigrate = true\n")
    f.write("placeholderReplacement = false\ncallbacks = []\n")
    f.write('ignoreMigrationPatterns = ["*:pending"]\n')
os.chmod(path, 0o600)
PY

cat > "$FIXTURES/V1__base.sql" <<'SQL'
CREATE TABLE gate_success.accepted_rows(id integer PRIMARY KEY, payload text NOT NULL);
INSERT INTO gate_success.accepted_rows VALUES (1, 'V1');
SQL
cat > "$FIXTURES/V2__second.sql" <<'SQL'
INSERT INTO gate_success.accepted_rows VALUES (2, 'V2');
SQL
cat > "$FIXTURES/R__row_count.sql" <<'SQL'
CREATE OR REPLACE VIEW gate_success.accepted_row_count AS
SELECT count(*)::integer AS row_count FROM gate_success.accepted_rows;
SQL

flyway() {
  local schema="$1" location="$2" output="$3" verb="$4"
  env -i PATH=/usr/bin:/bin HOME="$STATE_DIR/gate-ef-home" REDGATE_DISABLE_TELEMETRY=true "$RUNTIME/flyway" -configFiles="$CONFIG" -schemas="$schema" \
    -defaultSchema="$schema" -locations="filesystem:$location" \
    -outputType=json "$verb" >"$output" 2>&1 || return $?
  # Flyway validate may exit zero while validationSuccessful is false.
  # Match the application's semantic result gate rather than trusting exit code.
  python3 - "$output" "$verb" <<'PYJSON'
import json,sys
text=open(sys.argv[1]).read()
try:
    obj=json.JSONDecoder().raw_decode(text[text.index('{'):])[0]
    ok=obj.get('operation')==sys.argv[2] and not obj.get('error') and not obj.get('errorDetails')
    if sys.argv[2]=='validate': ok=ok and obj.get('validationSuccessful') is True and obj.get('invalidMigrations')==[]
    if sys.argv[2]=='migrate': ok=ok and obj.get('success') is True
    if sys.argv[2]=='info': ok=ok and isinstance(obj.get('migrations'),list)
except (ValueError,TypeError): ok=False
sys.exit(0 if ok else 1)
PYJSON
}
query() {
  local sql="$1"
  docker exec -e PGPASSWORD="$PGPASSWORD" -e PGSSLMODE=verify-full -e PGSSLROOTCERT=/tmp/ca.crt "$CONTAINER" psql -XAtq \
    -h localhost -U "$PGUSER" -d "$DATABASE" -c "$sql"
}
expect_fail() {
  local schema="$1" location="$2" output="$3" verb="$4"
  if flyway "$schema" "$location" "$output" "$verb"; then
    echo "Expected controlled $verb failure did not occur for $schema." >&2
    exit 1
  fi
}
assert_eq() {
  [[ "$1" == "$2" ]] || { printf 'Gate assertion failed (%s): expected %s, received %s\n' "$3" "$2" "$1" >&2; exit 1; }
}

# E1: V1/V2 plus repeatable; second submission applies no migration again.
success_dir="$FIXTURES/success"
mkdir "$success_dir"
cp "$FIXTURES"/{V1__base.sql,V2__second.sql,R__row_count.sql} "$success_dir/"
flyway gate_success "$success_dir" "$EVIDENCE/success-first.json" migrate
first_count=$(query "SELECT count(*) FROM gate_success.flyway_schema_history WHERE success")
flyway gate_success "$success_dir" "$EVIDENCE/success-repeat.json" migrate
second_count=$(query "SELECT count(*) FROM gate_success.flyway_schema_history WHERE success")
assert_eq "$first_count" 3 'V1/V2/repeatable applied once'
assert_eq "$second_count" "$first_count" 'repeat submission did not replay'
assert_eq "$(query 'SELECT row_count FROM gate_success.accepted_row_count')" 2 'V1/V2 data'

# E2: transactional failure rolls back the failed migration's DDL and DML.
rollback_dir="$FIXTURES/rollback"
mkdir "$rollback_dir"
cat > "$rollback_dir/V1__rollback.sql" <<'SQL'
CREATE TABLE gate_rollback.must_rollback(id integer PRIMARY KEY);
INSERT INTO gate_rollback.missing_table VALUES (1);
SQL
expect_fail gate_rollback "$rollback_dir" "$EVIDENCE/rollback-failed.json" migrate
assert_eq "$(query "SELECT count(*) FROM information_schema.tables WHERE table_schema='gate_rollback' AND table_name='must_rollback'")" 0 'transactional DDL rollback'

# E3: an earlier committed version remains after a later version fails.
partial_dir="$FIXTURES/partial"
mkdir "$partial_dir"
cat > "$partial_dir/V1__committed.sql" <<'SQL'
CREATE TABLE gate_partial.committed_rows(id integer PRIMARY KEY);
INSERT INTO gate_partial.committed_rows VALUES (1);
SQL
cat > "$partial_dir/V2__fail.sql" <<'SQL'
INSERT INTO gate_partial.committed_rows VALUES (1);
SQL
expect_fail gate_partial "$partial_dir" "$EVIDENCE/partial-failed.json" migrate
assert_eq "$(query 'SELECT count(*) FROM gate_partial.committed_rows')" 1 'earlier migration remains committed'
assert_eq "$(query "SELECT count(*) FROM gate_partial.flyway_schema_history WHERE version='1' AND success")" 1 'earlier success evidence'

# E4: checksum drift must fail validation and remain visible.
checksum_dir="$FIXTURES/checksum"
mkdir "$checksum_dir"
echo 'CREATE TABLE gate_checksum.checksum_probe(id integer PRIMARY KEY);' > "$checksum_dir/V1__checksum.sql"
flyway gate_checksum "$checksum_dir" "$EVIDENCE/checksum-first.json" migrate
echo '-- checksum drift fixture' >> "$checksum_dir/V1__checksum.sql"
expect_fail gate_checksum "$checksum_dir" "$EVIDENCE/checksum-mismatch.json" validate

# E5: the execution role cannot cross into a restricted schema.
isolation_dir="$FIXTURES/isolation"
mkdir "$isolation_dir"
cat > "$isolation_dir/V1__cross_schema.sql" <<'SQL'
INSERT INTO gate_other.guard VALUES (2, 'must be denied');
SQL
expect_fail gate_isolation "$isolation_dir" "$EVIDENCE/cross-schema-denied.json" migrate
assert_eq "$(docker exec "$CONTAINER" psql -XAtq -U lab_admin -d "$DATABASE" -c 'SELECT count(*) FROM gate_other.guard')" 1 'restricted schema unchanged'

# E6: capture artifact-relative Missing and native Future diagnostics.
future_dir="$FIXTURES/future"
mkdir "$future_dir"
echo 'CREATE TABLE gate_future.future_probe(id integer PRIMARY KEY);' > "$future_dir/V1__future.sql"
echo 'INSERT INTO gate_future.future_probe VALUES (2);' > "$future_dir/V2__second.sql"
flyway gate_future "$future_dir" "$EVIDENCE/future-before.json" migrate
mkdir "$FIXTURES/future-only-v1" "$FIXTURES/missing-v1"
cp "$future_dir/V1__future.sql" "$FIXTURES/future-only-v1/"
cp "$future_dir/V2__second.sql" "$FIXTURES/missing-v1/"
flyway gate_future "$FIXTURES/future-only-v1" "$EVIDENCE/future-info.json" info
flyway gate_future "$FIXTURES/missing-v1" "$EVIDENCE/missing-info.json" info

# E7: two simultaneous submissions must leave one successful version row.
concurrent_dir="$FIXTURES/concurrent"
mkdir "$concurrent_dir"
cat > "$concurrent_dir/V1__concurrent.sql" <<'SQL'
SELECT pg_sleep(4);
CREATE TABLE gate_concurrent.once(id integer PRIMARY KEY);
SQL
flyway gate_concurrent "$concurrent_dir" "$EVIDENCE/concurrent-a.json" migrate & pid_a=$!
flyway gate_concurrent "$concurrent_dir" "$EVIDENCE/concurrent-b.json" migrate & pid_b=$!
wait "$pid_a"
wait "$pid_b"
assert_eq "$(query "SELECT count(*) FROM gate_concurrent.flyway_schema_history WHERE version='1' AND success")" 1 'concurrent single application'

# E8: controlled client termination records what the CLI/database do. This is
# diagnostic only: it cannot prove Manager queue hold or restart semantics.
timeout_dir="$FIXTURES/timeout"
mkdir "$timeout_dir"
cat > "$timeout_dir/V1__long_transaction.sql" <<'SQL'
CREATE TABLE gate_timeout.transient_probe(id integer PRIMARY KEY);
SELECT pg_sleep(90);
INSERT INTO gate_timeout.transient_probe VALUES (1);
SQL
setsid env -i PATH=/usr/bin:/bin HOME="$STATE_DIR/gate-ef-home" REDGATE_DISABLE_TELEMETRY=true \
  "$RUNTIME/flyway" -configFiles="$CONFIG" -schemas=gate_timeout \
  -defaultSchema=gate_timeout -locations="filesystem:$timeout_dir" \
  -outputType=json migrate >"$EVIDENCE/timeout-terminated.stdout.log" 2>&1 &
client_pid=$!
observed_active=0
for _ in $(seq 1 45); do
  active=$(query "SELECT count(*) FROM pg_stat_activity WHERE datname='$DATABASE' AND usename='fwm_exec' AND state='active' AND query LIKE '%pg_sleep(90)%' AND pid<>pg_backend_pid()")
  if [[ "$active" != 0 ]]; then observed_active=1; break; fi
  if ! kill -0 "$client_pid" 2>/dev/null; then break; fi
  sleep 1
done
if [[ "$observed_active" != 1 ]]; then
  kill -TERM -- "-$client_pid" 2>/dev/null || true
  wait "$client_pid" 2>/dev/null || true
  echo 'Timed client did not reach the controlled long-running statement; no timeout acceptance claim.' >&2
  exit 1
fi
kill -TERM -- "-$client_pid"
set +e
wait "$client_pid"
timeout_status=$?
set -e
printf 'observed_active_statement=true\nterminated_exit=%s\n' "$timeout_status" > "$EVIDENCE/timeout-status.txt"
for _ in $(seq 1 10); do
  active=$(query "SELECT count(*) FROM pg_stat_activity WHERE datname='$DATABASE' AND usename='fwm_exec' AND state='active' AND query LIKE '%pg_sleep(90)%' AND pid<>pg_backend_pid()")
  [[ "$active" == 0 ]] && break
  sleep 1
done
assert_eq "$active" 0 'terminated client session cleaned up'
assert_eq "$(query "SELECT count(*) FROM information_schema.tables WHERE table_schema='gate_timeout' AND table_name='transient_probe'")" 0 'terminated transaction cleanup'

cat > "$EVIDENCE/coverage.txt" <<'EOF'
Direct pinned-CLI coverage prepared/executed by this script:
- PostgreSQL transactional V1/V2 and repeatable application; repeat migrate leaves successful history count unchanged.
- Transactional migration rollback; earlier committed migration retained after a later failure.
- Checksum mismatch validation; restricted cross-schema write denied.
- Artifact-relative Missing/Future info output; concurrent duplicate CLI invocation; client termination diagnostic.

This script does not test Flyway Manager APIs, browser behavior, queue idempotency,
database-qualified authorization, stream reservations/locks, held failure state,
automatic retry/repair denial, timeout cancellation policy, restart recovery, or
historical evidence snapshots. Those remain separate application acceptance gates.
The timeout observation is not a Manager recovery claim. PostgreSQL migration must
remain disabled until the application Gate E/F suite proves those behaviors.
EOF
chmod 600 "$CONFIG" "$EVIDENCE"/* 2>/dev/null || true
printf 'Gate E/F direct CLI suite completed against the disposable PostgreSQL lab. Private evidence: %s\n' "$EVIDENCE"
