#!/usr/bin/env bash
set -euo pipefail
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/flyway-manager-postgres-lab"
CONTAINER="flyway-manager-postgres17-lab"
if [[ ! -r "$STATE_DIR/lab.env" ]]; then
  echo 'Run setup.sh first.' >&2
  exit 1
fi
set -a
source "$STATE_DIR/lab.env"
set +a
lab_psql() {
  local user="$PGUSER" password="$PGPASSWORD" database="$PGDATABASE"
  if [[ "${1:-}" == readonly ]]; then user="$PGREADONLY_USER"; password="$PGREADONLY_PASSWORD"; shift; fi
  if [[ "${1:-}" == second ]]; then database="$PGSECOND_DATABASE"; shift; fi
  docker exec -e PGPASSWORD="$password" "$CONTAINER" psql -XAtq \
    -h localhost -U "$user" -d "$database" -c "$1"
}

version=$(docker exec "$CONTAINER" psql -XAtq -U lab_admin -d postgres -c 'SHOW server_version')
identity=$(lab_psql 'SELECT current_database() || chr(58) || current_user')
schemas=$(lab_psql "SELECT count(*) FROM information_schema.schemata WHERE schema_name IN ('app','restricted')")
second_identity=$(lab_psql second 'SELECT current_database() || chr(58) || current_user')
readonly_identity=$(lab_psql readonly 'SELECT current_database() || chr(58) || current_user')

if docker exec -e PGPASSWORD="$PGPASSWORD" "$CONTAINER" psql "host=127.0.0.1 dbname=$PGDATABASE user=$PGUSER sslmode=verify-full sslrootcert=/lab-certs/ca.crt connect_timeout=4" -XAtqc 'SELECT 1' >/dev/null 2>&1; then
  echo 'Wrong TLS hostname unexpectedly succeeded.' >&2
  exit 1
fi
if docker exec -e PGPASSWORD=deliberately-wrong "$CONTAINER" psql "host=localhost dbname=$PGDATABASE user=$PGUSER sslmode=verify-full sslrootcert=/lab-certs/ca.crt connect_timeout=4" -XAtqc 'SELECT 1' >/dev/null 2>&1; then
  echo 'Wrong password unexpectedly succeeded.' >&2
  exit 1
fi
if docker exec -e PGPASSWORD="$PGPASSWORD" "$CONTAINER" psql "host=localhost dbname=$PGDATABASE user=$PGUSER sslmode=verify-full sslrootcert=/nonexistent-ca.crt connect_timeout=4" -XAtqc 'SELECT 1' >/dev/null 2>&1; then
  echo 'Unknown CA unexpectedly succeeded.' >&2
  exit 1
fi
if lab_psql "SELECT has_schema_privilege(current_user, 'restricted', 'USAGE')" | rg -q '^t$'; then
  echo 'Execution role unexpectedly has restricted schema access.' >&2
  exit 1
fi
if lab_psql readonly "SELECT has_schema_privilege(current_user, 'app', 'USAGE') AND has_schema_privilege(current_user, 'app', 'CREATE')" | rg -q '^t$'; then
  echo 'Read-only role unexpectedly has app CREATE.' >&2
  exit 1
fi

image_id=$(docker inspect "$CONTAINER" --format '{{.Image}}')
printf 'PASS PostgreSQL %s; identity=%s; app/restricted schemas=%s; second=%s; readonly=%s; wrong hostname and unknown CA rejected.\n' \
  "$version" "$identity" "$schemas" "$second_identity" "$readonly_identity"
printf 'Image ID: %s\n' "$image_id"
