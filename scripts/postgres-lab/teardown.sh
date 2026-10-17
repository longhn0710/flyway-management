#!/usr/bin/env bash
set -euo pipefail
CONTAINER="flyway-manager-postgres17-lab"
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/flyway-manager-postgres-lab"
if docker container inspect "$CONTAINER" >/dev/null 2>&1; then
  docker rm -fv "$CONTAINER" >/dev/null
fi
rm -rf -- "$STATE_DIR"
printf 'Disposable PostgreSQL lab and private state removed.\n'
