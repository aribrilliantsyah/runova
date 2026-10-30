#!/usr/bin/env bash
# Local stack for the end-to-end test: PostgreSQL 18 and Caddy in containers
# (host network), Runova built from this checkout and running as the current user.
#
#   test/e2e/stack.sh up | down | stop-runova | start-runova | restart-runova
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=test/e2e/env.sh
source "$here/env.sh"

PIDFILE="$STATE/runova.pid"
LOG="$STATE/runova.log"

die() {
	echo "stack: $*" >&2
	exit 1
}

need() {
	local c
	for c in "$@"; do
		command -v "$c" >/dev/null || die "perintah '$c' tidak ditemukan"
	done
}

# wait_for SECONDS DESCRIPTION COMMAND...
wait_for() {
	local secs=$1 what=$2 i
	shift 2
	for ((i = 0; i < secs; i++)); do
		if "$@" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	die "$what tidak siap dalam ${secs} detik"
}

tcp_open() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

psql_admin() {
	docker exec -i "$PG_CONTAINER" psql -X -q -v ON_ERROR_STOP=1 -U postgres -p "$PG_PORT"
}

up_postgres() {
	mkdir -p "$STATE/hba.d"
	chmod 755 "$STATE" "$STATE/hba.d"
	# Like the installed server: loopback for Runova's admin role, then Runova's own pg_hba.d.
	cat >"$STATE/pg_hba.conf" <<EOF
local all all trust
host all all 127.0.0.1/32 scram-sha-256
include_dir hba.d
EOF
	chmod 644 "$STATE/pg_hba.conf"
	docker run -d --name "$PG_CONTAINER" --network host \
		-e POSTGRES_PASSWORD="$PG_PASSWORD" \
		-v "$STATE/pg_hba.conf:/etc/e2e/pg_hba.conf:ro" \
		-v "$STATE/hba.d:/etc/e2e/hba.d:ro" \
		postgres:18 -c "port=$PG_PORT" -c "listen_addresses=*" -c hba_file=/etc/e2e/pg_hba.conf >/dev/null
	# The init-time server listens on the socket only, so an open TCP port means the real one is up.
	wait_for 90 "PostgreSQL" tcp_open "$PG_PORT"
	psql_admin <<EOF
CREATE ROLE runova LOGIN CREATEDB CREATEROLE PASSWORD '$PG_PASSWORD';
GRANT EXECUTE ON FUNCTION pg_reload_conf() TO runova;
EOF
}

up_caddy() {
	printf '{\n\tadmin localhost:2019\n}\n' >"$STATE/Caddyfile"
	chmod 644 "$STATE/Caddyfile"
	docker run -d --name "$CADDY_CONTAINER" --network host \
		-v "$STATE/Caddyfile:/etc/caddy/Caddyfile:ro" \
		-v "${CADDY_VOLUMES[0]}:/config" -v "${CADDY_VOLUMES[1]}:/data" \
		caddy:2 caddy run --resume --config /etc/caddy/Caddyfile >/dev/null
	wait_for 30 "Caddy admin API" curl -sf http://127.0.0.1:2019/config/
}

build_runova() {
	mkdir -p "$CACHE"
	(cd "$REPO" && go build -o "$RUNOVA_BIN" ./cmd/runova)
}

runova_running() {
	[[ -f $PIDFILE ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null
}

start_runova() {
	runova_running && die "Runova sudah berjalan (pid $(cat "$PIDFILE"))"
	setsid nohup "$RUNOVA_BIN" serve >>"$LOG" 2>&1 &
	echo $! >"$PIDFILE"
	wait_for 30 "Runova" curl -s -o /dev/null "http://$RUNOVA_LISTEN/api/me"
	# The panel is served through Caddy once the syncer has loaded the configuration.
	wait_for 30 "panel lewat Caddy (https://127.0.0.1:$PANEL_PORT)" curl -sk -o /dev/null "https://127.0.0.1:$PANEL_PORT/api/me"
}

stop_runova() {
	local pid i
	[[ -f $PIDFILE ]] || return 0
	pid=$(cat "$PIDFILE")
	if kill -0 "$pid" 2>/dev/null; then
		kill -TERM "$pid"
		# Shutdown waits up to 30 s for background deploys.
		for ((i = 0; i < 45; i++)); do
			kill -0 "$pid" 2>/dev/null || break
			sleep 1
		done
		if kill -0 "$pid" 2>/dev/null; then
			kill -KILL "$pid"
		fi
	fi
	rm -f "$PIDFILE"
}

# abort_up runs on any exit before `up` finished and removes the half-built stack.
abort_up() {
	local status=$?
	trap - EXIT
	echo "stack: gagal naik; membersihkan" >&2
	down
	exit "$status"
}

up() {
	need docker curl jq go setsid
	if docker container inspect "$PG_CONTAINER" "$CADDY_CONTAINER" >/dev/null 2>&1 || [[ -d $STATE ]]; then
		die "stack sudah ada atau sisa lama tertinggal; jalankan 'test/e2e/stack.sh down' dulu"
	fi
	for image in postgres:18 caddy:2 gcr.io/distroless/static-debian12 eclipse-temurin:21-jre; do
		docker image inspect "$image" >/dev/null 2>&1 || docker pull -q "$image" >/dev/null
	done
	mkdir -p "$STATE/data"
	trap abort_up EXIT
	up_postgres
	up_caddy
	build_runova
	"$RUNOVA_BIN" keygen
	"$RUNOVA_BIN" panel set --port "$PANEL_PORT"
	start_runova
	trap - EXIT
	echo "stack siap: panel https://127.0.0.1:$PANEL_PORT (log: $LOG)"
}

# remove_state deletes the state folder; files that containers wrote there belong to another UID.
remove_state() {
	[[ -d $STATE ]] || return 0
	rm -rf "$STATE" 2>/dev/null && return 0
	docker run --rm -v "$STATE:/state" --entrypoint rm ubuntu:26.04 -rf /state/data /state/hba.d /state/secret.key
	rm -rf "$STATE"
}

down() {
	local ids nets
	stop_runova
	ids=$(docker ps -aq --filter label=runova.app)
	if [[ -n $ids ]]; then
		# shellcheck disable=SC2086
		docker rm -f $ids >/dev/null
	fi
	docker rm -f "$PG_CONTAINER" "$CADDY_CONTAINER" >/dev/null 2>&1 || true
	nets=$(docker network ls --format '{{.Name}}' | grep -E '^runova-u[0-9]+$' || true)
	if [[ -n $nets ]]; then
		# shellcheck disable=SC2086
		docker network rm $nets >/dev/null
	fi
	docker volume rm "${CADDY_VOLUMES[@]}" >/dev/null 2>&1 || true
	remove_state
	local left
	left=$(docker ps -a --filter name=runova- --format '{{.Names}}')
	[[ -z $left ]] || die "container runova-* tersisa: $left"
	echo "stack dibersihkan"
}

case "${1:-}" in
up) up ;;
down) down ;;
stop-runova) stop_runova ;;
start-runova) start_runova ;;
restart-runova)
	stop_runova
	start_runova
	;;
*) die "penggunaan: stack.sh up | down | stop-runova | start-runova | restart-runova" ;;
esac
