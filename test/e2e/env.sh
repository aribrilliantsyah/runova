# shellcheck shell=bash disable=SC2034
# Shared settings of the local end-to-end stack; sourced by stack.sh and run.sh.

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$E2E_DIR/../.." && pwd)"
CACHE="$E2E_DIR/.cache"
STATE="$CACHE/state"
RUNOVA_BIN="$CACHE/runova"

PG_CONTAINER=runova-e2e-pg
CADDY_CONTAINER=runova-e2e-caddy
CADDY_VOLUMES=(runova-e2e-caddy-config runova-e2e-caddy-data)
# 5432 is often taken on a development machine; containers reach this port through host.docker.internal.
PG_PORT="${E2E_PG_PORT:-55432}"
PG_PASSWORD=e2e-runova-pg-password
PANEL_BACKEND_PORT="${E2E_BACKEND_PORT:-7070}"
PANEL_PORT="${E2E_PANEL_PORT:-8443}"

export RUNOVA_LISTEN="127.0.0.1:$PANEL_BACKEND_PORT"
export RUNOVA_DATA="$STATE/data"
export RUNOVA_SECRET_KEY="$STATE/secret.key"
export RUNOVA_PG_DSN="host=127.0.0.1 port=$PG_PORT user=runova password=$PG_PASSWORD dbname=postgres"
export RUNOVA_PG_HBA="$STATE/hba.d/runova.conf"
# The development process is not root, so it cannot chown app data folders to the container UID.
export RUNOVA_DEV_CHMOD=1
