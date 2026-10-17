#!/usr/bin/env bash
set -euo pipefail
umask 077

STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/flyway-manager-postgres-lab"
CONTAINER="flyway-manager-postgres17-lab"
IMAGE='postgres@sha256:2d2b8998d31037bf721cfdf764d76ba74171b4fab3431b7f72c27c56ddbdf9e3'
PORT=55440

if ! command -v docker >/dev/null || ! docker info >/dev/null 2>&1; then
  echo 'Docker daemon is unavailable.' >&2
  exit 1
fi
if docker container inspect "$CONTAINER" >/dev/null 2>&1; then
  echo 'Lab container already exists; use teardown.sh before recreating it.' >&2
  exit 1
fi
mkdir -p "$STATE_DIR/certs"
chmod 700 "$STATE_DIR" "$STATE_DIR/certs"

openssl rand -hex 24 > "$STATE_DIR/postgres_password"
openssl rand -hex 24 > "$STATE_DIR/execution_password"
openssl rand -hex 24 > "$STATE_DIR/readonly_password"
openssl req -x509 -newkey rsa:3072 -nodes -sha256 -days 365 \
  -subj '/CN=Flyway Manager PostgreSQL Lab CA' \
  -keyout "$STATE_DIR/certs/ca.key" -out "$STATE_DIR/certs/ca.crt" >/dev/null 2>&1
openssl req -new -newkey rsa:3072 -nodes -sha256 \
  -subj '/CN=localhost' -keyout "$STATE_DIR/certs/server.key" \
  -out "$STATE_DIR/certs/server.csr" >/dev/null 2>&1
cat > "$STATE_DIR/certs/server.ext" <<'EOF'
subjectAltName=DNS:localhost
extendedKeyUsage=serverAuth
keyUsage=digitalSignature,keyEncipherment
EOF
openssl x509 -req -sha256 -days 365 -in "$STATE_DIR/certs/server.csr" \
  -CA "$STATE_DIR/certs/ca.crt" -CAkey "$STATE_DIR/certs/ca.key" \
  -CAcreateserial -extfile "$STATE_DIR/certs/server.ext" \
  -out "$STATE_DIR/certs/server.crt" >/dev/null 2>&1
rm -f "$STATE_DIR/certs/server.csr" "$STATE_DIR/certs/server.ext" "$STATE_DIR/certs/ca.srl"
chmod 600 "$STATE_DIR/certs/ca.key" "$STATE_DIR/certs/server.key"
chmod 644 "$STATE_DIR/certs/server.key" "$STATE_DIR/certs/server.crt" "$STATE_DIR/certs/ca.crt"
chmod 755 "$STATE_DIR/certs"

cat > "$STATE_DIR/pg_hba.conf" <<'EOF'
local all all trust
hostnossl all all 0.0.0.0/0 reject
hostnossl all all ::/0 reject
hostssl all all 0.0.0.0/0 scram-sha-256
hostssl all all ::/0 scram-sha-256
EOF
chmod 644 "$STATE_DIR/pg_hba.conf"

docker run -d --name "$CONTAINER" --restart no \
  -p "127.0.0.1:${PORT}:5432" \
  -e POSTGRES_USER=lab_admin -e POSTGRES_PASSWORD="$(cat "$STATE_DIR/postgres_password")" \
  -e POSTGRES_DB=postgres \
  -v "$STATE_DIR/certs:/lab-certs:ro" -v "$STATE_DIR/pg_hba.conf:/lab-pg-hba.conf:ro" \
  --user 0:0 --entrypoint bash "$IMAGE" -ec 'cp /lab-certs/server.key /tmp/server.key; cp /lab-certs/server.crt /tmp/server.crt; cp /lab-certs/ca.crt /tmp/ca.crt; chown 999:999 /tmp/server.key /tmp/server.crt /tmp/ca.crt; chmod 600 /tmp/server.key; exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key -c ssl_ca_file=/tmp/ca.crt -c password_encryption=scram-sha-256 -c hba_file=/lab-pg-hba.conf' >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$CONTAINER" pg_isready -U lab_admin -d postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$CONTAINER" pg_isready -U lab_admin -d postgres >/dev/null

admin_password=$(cat "$STATE_DIR/postgres_password")
exec_password=$(cat "$STATE_DIR/execution_password")
readonly_password=$(cat "$STATE_DIR/readonly_password")
sql_file=$(mktemp "$STATE_DIR/init.XXXXXX.sql")
trap 'rm -f "$sql_file"; unset admin_password exec_password readonly_password' EXIT
cat > "$sql_file" <<SQL
DO \$\$ BEGIN CREATE ROLE fwm_exec LOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END \$\$;
DO \$\$ BEGIN CREATE ROLE fwm_readonly LOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END \$\$;
ALTER ROLE fwm_exec PASSWORD '${exec_password}';
ALTER ROLE fwm_readonly PASSWORD '${readonly_password}';
SQL
docker exec -e PGPASSWORD="$admin_password" -i "$CONTAINER" \
  psql -v ON_ERROR_STOP=1 -U lab_admin -d postgres < "$sql_file" >/dev/null
for database in fwm_test fwm_test_second; do
  if ! docker exec -e PGPASSWORD="$admin_password" "$CONTAINER" \
    psql -XAtq -U lab_admin -d postgres -c "SELECT 1 FROM pg_database WHERE datname='${database}'" | rg -q '^1$'; then
    docker exec -e PGPASSWORD="$admin_password" "$CONTAINER" \
      createdb -U lab_admin -O lab_admin "$database"
  fi
done
cat > "$sql_file" <<'SQL'
REVOKE ALL ON DATABASE fwm_test FROM PUBLIC;
GRANT CONNECT ON DATABASE fwm_test TO fwm_exec, fwm_readonly;
CREATE SCHEMA IF NOT EXISTS app AUTHORIZATION fwm_exec;
ALTER SCHEMA app OWNER TO fwm_exec;
CREATE SCHEMA IF NOT EXISTS restricted AUTHORIZATION lab_admin;
ALTER SCHEMA restricted OWNER TO lab_admin;
REVOKE ALL ON SCHEMA restricted FROM PUBLIC;
CREATE TABLE IF NOT EXISTS restricted.lab_marker(id integer PRIMARY KEY, note text NOT NULL);
INSERT INTO restricted.lab_marker VALUES (1, 'disposable PostgreSQL lab') ON CONFLICT (id) DO NOTHING;
GRANT USAGE ON SCHEMA app TO fwm_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA app TO fwm_readonly;
ALTER DEFAULT PRIVILEGES FOR ROLE fwm_exec IN SCHEMA app GRANT SELECT ON TABLES TO fwm_readonly;
SQL
docker exec -e PGPASSWORD="$admin_password" -i "$CONTAINER" \
  psql -v ON_ERROR_STOP=1 -U lab_admin -d fwm_test < "$sql_file" >/dev/null
cat > "$sql_file" <<'SQL'
CREATE SCHEMA IF NOT EXISTS app AUTHORIZATION fwm_exec;
ALTER SCHEMA app OWNER TO fwm_exec;
SQL
docker exec -e PGPASSWORD="$admin_password" -i "$CONTAINER" \
  psql -v ON_ERROR_STOP=1 -U lab_admin -d fwm_test_second < "$sql_file" >/dev/null
rm -f "$sql_file"

cat > "$STATE_DIR/lab.env" <<EOF
PGHOST=localhost
PGPORT=${PORT}
PGDATABASE=fwm_test
PGUSER=fwm_exec
PGPASSWORD=${exec_password}
PGSSLMODE=verify-full
PGSSLROOTCERT=${STATE_DIR}/certs/ca.crt
PGSECOND_DATABASE=fwm_test_second
PGREADONLY_USER=fwm_readonly
PGREADONLY_PASSWORD=${readonly_password}
EOF
chmod 600 "$STATE_DIR/lab.env" "$STATE_DIR/postgres_password" "$STATE_DIR/execution_password" "$STATE_DIR/readonly_password"
printf 'PostgreSQL lab is ready. Private state: %s; local port: %s; image: postgres:17 (digest recorded in setup.sh).\n' "$STATE_DIR" "$PORT"
