#!/usr/bin/env bash
# Installs Runova for real on a fresh Ubuntu 26.04 with systemd as PID 1 (a
# privileged container, no VM) through scripts/deploy.sh, then exercises it:
# check, login, user + database, a deployed binary reached through its IP port,
# a second deploy (idempotent, no new password), a restart of the services and
# a reboot of the whole server (the app must come back by itself).
#
#   test/system/run.sh            # needs docker, go, curl, jq, ssh; no root
#   KEEP=1 test/system/run.sh     # leave the container for inspection
#
# Docker runs inside the container (Docker-in-Docker), so the first run pulls
# the apt packages and the app's base image from the internet.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"

NAME="${SYSTEM_CONTAINER:-runova-system-test}"
IMAGE=runova-system
SSH_PORT="${SYSTEM_SSH_PORT:-2222}"
SSH_USER=deploy
WORK="$(mktemp -d)"
SCENARIO=""
CODE=""
BODY=""
ADMIN_PASSWORD=""
SSH_TEST_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes)
export RUNOVA_SSH_OPTS="${SSH_TEST_OPTS[*]}"

cleanup() {
	local status=$?
	if [[ ${KEEP:-0} == 1 ]]; then
		printf 'KEEP=1: container %s tetap ada (ssh -p %s -i %s %s@127.0.0.1)\n' "$NAME" "$SSH_PORT" "$WORK/id" "$SSH_USER" >&2
		return "$status"
	fi
	remove_container
	rm -rf "$WORK"
	return "$status"
}
trap cleanup EXIT

# remove_container stops the inner Docker's containers first so nothing of them outlives the outer container.
remove_container() {
	if docker container inspect "$NAME" >/dev/null 2>&1; then
		docker exec "$NAME" sh -c 'command -v docker >/dev/null && { docker ps -aq | xargs -r docker rm -f; systemctl stop docker.socket docker; }' >/dev/null 2>&1 || true
		docker rm -f -v "$NAME" >/dev/null 2>&1 || true
	fi
}

fail() {
	printf '\nGAGAL [%s]: %s\n' "$SCENARIO" "$*" >&2
	if docker container inspect "$NAME" >/dev/null 2>&1; then
		printf -- '--- journalctl -u runova -u caddy -u postgresql (30 baris terakhir) ---\n' >&2
		docker exec "$NAME" journalctl -u runova -u caddy -u postgresql -n 30 --no-pager >&2 2>&1 || true
	fi
	exit 1
}

scenario() {
	SCENARIO=$1
	printf '== %s\n' "$1"
}

pass() { printf '   ok: %s\n' "$*"; }

need() {
	local c
	for c in "$@"; do
		command -v "$c" >/dev/null || fail "perintah '$c' tidak ditemukan"
	done
}

in_box() { docker exec "$NAME" "$@"; }

# as_runova SUBCOMMAND...: the installed binary as the service user, like the installer runs it.
as_runova() {
	in_box runuser -u runova -- env RUNOVA_DATA=/var/lib/runova RUNOVA_SECRET_KEY=/etc/runova/secret.key /usr/local/bin/runova "$@"
}

box_ip() { docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$NAME"; }

# req JAR METHOD PATH [JSON]: sets CODE and BODY.
req() {
	local jar=$1 method=$2 path=$3 data=${4-} out
	local args=(-sk -X "$method" -b "$jar" -c "$jar" -m 120 -w $'\n%{http_code}')
	if [[ -n $data ]]; then
		args+=(-H 'Content-Type: application/json' --data "$data")
	fi
	out=$(curl "${args[@]}" "$PANEL$path") || fail "curl $method $path gagal"
	CODE=${out##*$'\n'}
	BODY=${out%$'\n'*}
}

expect() { [[ $CODE == "$1" ]] || fail "status $CODE (diharapkan $1): $BODY"; }

curl_ok() {
	local url=$1 tries=${2:-30} i out
	for ((i = 0; i < tries; i++)); do
		if out=$(curl -sf -m 5 "$url" 2>/dev/null); then
			printf '%s' "$out"
			return 0
		fi
		sleep 1
	done
	fail "GET $url tidak menjawab 200"
}

wait_app() {
	local jar=$1 id=$2 i status busy
	for ((i = 0; i < 240; i++)); do
		req "$jar" GET "/api/apps/$id"
		expect 200
		status=$(jq -r .app.status <<<"$BODY")
		busy=$(jq -r .busy <<<"$BODY")
		if [[ $status == failed ]]; then
			fail "aplikasi $id gagal: $(jq -r '.deployment.failure_log // ""' <<<"$BODY")"
		fi
		if [[ $status == running && $busy == false ]]; then
			return 0
		fi
		sleep 1
	done
	fail "aplikasi $id tidak running dalam 240 detik (status $status)"
}

deploy() {
	"$repo/scripts/deploy.sh" -p "$SSH_PORT" -i "$WORK/id" "$@"
}

need docker go curl jq ssh ssh-keygen

# ---------------------------------------------------------------------------
scenario "0 server baru"
remove_container
docker build -q -t "$IMAGE" -f "$here/Dockerfile" "$here" >/dev/null || fail "build image gagal"
ssh-keygen -q -t ed25519 -N '' -f "$WORK/id"
docker run -d --name "$NAME" --privileged --cgroupns=host \
	-v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock \
	-v /var/lib/docker -v /var/lib/containerd \
	-p "127.0.0.1:$SSH_PORT:22" "$IMAGE" >/dev/null || fail "container tidak mau jalan (port $SSH_PORT dipakai? set SYSTEM_SSH_PORT)"
docker cp "$WORK/id.pub" "$NAME:/home/$SSH_USER/.ssh/authorized_keys"
in_box sh -c "chown $SSH_USER:$SSH_USER /home/$SSH_USER/.ssh/authorized_keys && chmod 600 /home/$SSH_USER/.ssh/authorized_keys"
for _ in $(seq 60); do
	[[ $(in_box systemctl is-system-running 2>/dev/null || true) =~ ^(running|degraded)$ ]] && break
	sleep 1
done
for _ in $(seq 30); do
	ssh -p "$SSH_PORT" -i "$WORK/id" "${SSH_TEST_OPTS[@]}" "$SSH_USER@127.0.0.1" true 2>/dev/null && break
	sleep 1
done
ssh -p "$SSH_PORT" -i "$WORK/id" "${SSH_TEST_OPTS[@]}" "$SSH_USER@127.0.0.1" true || fail "ssh ke container tidak tersambung"
IP=$(box_ip)
PANEL="https://$IP:8443"
os_version=$(in_box sh -c ". /etc/os-release && echo \"\$VERSION_ID\"")
pass "Ubuntu $os_version dengan systemd ($(in_box systemctl is-system-running || true)), ssh di 127.0.0.1:$SSH_PORT, IP $IP"

# ---------------------------------------------------------------------------
scenario "1 deploy.sh memasang"
out=$(deploy --version 0.0.1-systest "$SSH_USER@127.0.0.1" --yes 2>&1) || {
	printf '%s\n' "$out" >&2
	fail "scripts/deploy.sh gagal"
}
printf '%s\n' "$out" | tail -n 25 | sed 's/^/   | /'
ADMIN_PASSWORD=$(sed -n 's/^Password     : //p' <<<"$out" | tr -d '\r' | head -n 1)
[[ -n $ADMIN_PASSWORD ]] || fail "password admin tidak tercetak"
[[ $(in_box /usr/local/bin/runova version) == "runova 0.0.1-systest" ]] || fail "versi terpasang: $(in_box /usr/local/bin/runova version)"
# The script cleans its folder under /tmp on the server, also when it succeeds.
left=$(in_box sh -c 'ls -d /tmp/runova-* 2>/dev/null' || true)
[[ -z $left ]] || fail "folder sementara tertinggal di server: $left"
pass "terpasang 0.0.1-systest; password admin tercetak; folder /tmp bersih"

# ---------------------------------------------------------------------------
scenario "2 runova check dan layanan"
check=$(as_runova check) || fail "runova check: $check"
printf '%s\n' "$check" | sed 's/^/   | /'
[[ $(grep -c '^ok ' <<<"$check") -ge 7 ]] || fail "kurang dari 7 baris ok: $check"
for svc in docker caddy postgresql runova; do
	[[ $(in_box systemctl is-active "$svc") == active ]] || fail "$svc tidak aktif"
	[[ $(in_box systemctl is-enabled "$svc") == enabled ]] || fail "$svc tidak enabled"
done
ufw=$(in_box ufw status)
for port in 22/tcp 80/tcp 443/tcp 20000:20999/tcp 5432/tcp 8443/tcp; do
	grep -q "^$port " <<<"$ufw" || fail "ufw belum membuka $port: $ufw"
done
pass "runova check ok semua; docker, caddy, postgresql, runova aktif dan enabled; ufw aktif dengan port yang benar"

# ---------------------------------------------------------------------------
scenario "3 login panel"
ADMIN_JAR="$WORK/admin.jar"
BUDI_JAR="$WORK/budi.jar"
req "$ADMIN_JAR" POST /api/login "$(jq -nc --arg p "$ADMIN_PASSWORD" '{username: "admin", password: $p}')"
expect 200
req "$ADMIN_JAR" GET /api/server
expect 200
for svc in docker caddy postgres; do
	[[ $(jq -r ".$svc.ok" <<<"$BODY") == true ]] || fail "halaman Server: $svc tidak sehat: $BODY"
done
pass "login admin lewat https://$IP:8443 (Caddy), Docker/Caddy/PostgreSQL sehat"

# ---------------------------------------------------------------------------
scenario "4 pengguna, database, deploy"
BUDI_PASSWORD="budi-password-123"
req "$ADMIN_JAR" POST /api/users "$(jq -nc --arg p "$BUDI_PASSWORD" '{username: "budi", password: $p, role: "user"}')"
expect 201
req "$BUDI_JAR" POST /api/login "$(jq -nc --arg p "$BUDI_PASSWORD" '{username: "budi", password: $p}')"
expect 200
req "$BUDI_JAR" POST /api/databases '{"suffix":"app"}'
expect 201
DB_ID=$(jq -r .id <<<"$BODY")
[[ $(jq -r .name <<<"$BODY") == budi_app ]] || fail "nama database: $BODY"
in_box runuser -u postgres -- psql -X -At -c "select datname from pg_database where datname='budi_app'" | grep -qx budi_app || fail "budi_app tidak ada di PostgreSQL"

hello="$WORK/hello"
mkdir -p "$hello"
(cd "$repo" && CGO_ENABLED=0 go build -ldflags "-X main.version=sys" -o "$hello/hello" ./test/e2e/hello)
printf 'PORT=8080\n' >"$hello/.env.example"
tar -czf "$WORK/hello.tar.gz" -C "$hello" hello .env.example
out=$(curl -sk -b "$BUDI_JAR" -c "$BUDI_JAR" -m 300 -w $'\n%{http_code}' -F "file=@$WORK/hello.tar.gz" "$PANEL/api/uploads") || fail "unggah gagal"
CODE=${out##*$'\n'}
BODY=${out%$'\n'*}
expect 201
UPLOAD=$(jq -r .id <<<"$BODY")
[[ $(jq -r .report.kind <<<"$BODY") == static ]] || fail "laporan unggahan: $BODY"
req "$BUDI_JAR" POST /api/apps "$(jq -nc --arg up "$UPLOAD" --argjson db "$DB_ID" \
	'{upload_id: $up, name: "hello", internal_port: 8080, memory_mb: 128, ip_access: true, env: [], database_id: $db}')"
expect 201
APP_ID=$(jq -r .app.id <<<"$BODY")
IP_PORT=$(jq -r .app.ip_port <<<"$BODY")
wait_app "$BUDI_JAR" "$APP_ID"
[[ $(curl_ok "http://$IP:$IP_PORT/" 60) == "hello sys" ]] || fail "GET / lewat port akses IP"
[[ $(curl_ok "http://$IP:$IP_PORT/db?add=1") == rows=1 ]] || fail "GET /db"
pass "budi + budi_app dibuat; hello berjalan di container (Docker nyata), dijangkau dari luar di $IP:$IP_PORT dan terhubung ke PostgreSQL apt"

# ---------------------------------------------------------------------------
scenario "5 deploy.sh kedua kali (pembaruan)"
key_before=$(in_box sha256sum /etc/runova/secret.key)
out=$(deploy --version 0.0.2-systest "$SSH_USER@127.0.0.1" --yes 2>&1) || {
	printf '%s\n' "$out" >&2
	fail "scripts/deploy.sh kedua gagal"
}
printf '%s\n' "$out" | tail -n 8 | sed 's/^/   | /'
[[ $out != *Password* ]] || fail "pemasangan ulang mencetak password"
[[ $(in_box /usr/local/bin/runova version) == "runova 0.0.2-systest" ]] || fail "versi setelah pembaruan: $(in_box /usr/local/bin/runova version)"
[[ $(in_box sha256sum /etc/runova/secret.key) == "$key_before" ]] || fail "secret.key berubah"
req "$ADMIN_JAR" POST /api/login "$(jq -nc --arg p "$ADMIN_PASSWORD" '{username: "admin", password: $p}')"
expect 200
[[ $(in_box runuser -u postgres -- psql -X -At -c "select count(*) from pg_roles where rolname='runova'") == 1 ]] || fail "role runova"
[[ $(curl_ok "http://$IP:$IP_PORT/db") == rows=1 ]] || fail "data aplikasi hilang setelah pembaruan"
wait_app "$BUDI_JAR" "$APP_ID"
pass "0.0.2-systest terpasang tanpa password baru; kunci, password admin, data database, dan aplikasi utuh"

# ---------------------------------------------------------------------------
scenario "6 restart layanan"
in_box systemctl restart runova caddy postgresql
for _ in $(seq 90); do
	[[ $(curl -sk -m 3 -o /dev/null -w '%{http_code}' "$PANEL/api/me") =~ ^(200|401)$ ]] && break
	sleep 1
done
req "$BUDI_JAR" GET /api/me
[[ $CODE == 200 || $CODE == 401 ]] || fail "panel tidak menjawab setelah restart: $CODE"
req "$ADMIN_JAR" POST /api/login "$(jq -nc --arg p "$ADMIN_PASSWORD" '{username: "admin", password: $p}')"
expect 200
[[ $(curl_ok "http://$IP:$IP_PORT/db?add=1" 60) == rows=2 ]] || fail "aplikasi/database tidak pulih setelah restart"
wait_app "$BUDI_JAR" "$APP_ID"
as_runova check >/dev/null || fail "runova check gagal setelah restart"
pass "restart runova caddy postgresql: panel, aplikasi (rows=2), dan check pulih"

# ---------------------------------------------------------------------------
scenario "6b reboot server"
# `docker restart` of the systemd container is the closest thing to a reboot
# here: systemd (PID 1) gets SIGRTMIN+3, stops every unit in order (dockerd
# SIGTERMs the app container, which hello answers with exit 0, the case
# on-failure never restarts) and boots again on the same disks (/var/lib/docker
# is a volume). Not covered: firmware, kernel, the host's own network start.
[[ $(in_box docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$(in_box docker ps -q --filter label=runova.app="$APP_ID")") == on-failure ]] || fail "container hello tidak memakai on-failure"
in_box docker ps -q --filter label=runova.app="$APP_ID" | grep -q . || fail "container hello tidak berjalan sebelum reboot"
docker restart -t 120 "$NAME" >/dev/null || fail "docker restart $NAME gagal"
for _ in $(seq 120); do
	[[ $(in_box systemctl is-system-running 2>/dev/null || true) =~ ^(running|degraded)$ ]] && break
	sleep 1
done
[[ $(box_ip) == "$IP" ]] || fail "IP container berubah setelah reboot: $(box_ip) != $IP"
for svc in docker caddy postgresql runova; do
	[[ $(in_box systemctl is-active "$svc") == active ]] || fail "$svc tidak aktif setelah reboot"
done
for _ in $(seq 120); do
	[[ $(curl -sk -m 3 -o /dev/null -w '%{http_code}' "$PANEL/api/me") =~ ^(200|401)$ ]] && break
	sleep 1
done
req "$ADMIN_JAR" POST /api/login "$(jq -nc --arg p "$ADMIN_PASSWORD" '{username: "admin", password: $p}')"
expect 200
[[ $(curl_ok "http://$IP:$IP_PORT/db?add=1" 120) == rows=3 ]] || fail "aplikasi tidak hidup lagi setelah reboot (rows=3 diharapkan)"
wait_app "$BUDI_JAR" "$APP_ID"
req "$BUDI_JAR" GET /api/notifications
expect 200
[[ $(jq '[.. | objects | select(.title? == "Aplikasi hello dinyalakan lagi setelah server dimulai ulang")] | length' <<<"$BODY") == 1 ]] || fail "notifikasi dinyalakan lagi tidak tepat satu: $BODY"
as_runova check >/dev/null || fail "runova check gagal setelah reboot"
pass "reboot server: panel 200, hello dinyalakan lagi oleh Runova (exit 0 saat SIGTERM), rows=3, satu notifikasi"

# ---------------------------------------------------------------------------
scenario "7 runova update lalu rollback"
REL="$WORK/release"
REL_REPO=me/runova
REL_VERSION=0.0.3-systest
REL_PORT=18080
rel_dir="$REL/$REL_REPO/releases/download/v$REL_VERSION"
mkdir -p "$rel_dir"
(cd "$repo" && scripts/release.sh "$REL_VERSION" >/dev/null) || fail "scripts/release.sh gagal"
cp "$repo/dist/runova-$REL_VERSION-linux-amd64.tar.gz" "$repo/dist/SHA256SUMS" "$rel_dir/"
cp "$repo/get.sh" "$rel_dir/get.sh"
(cd "$rel_dir" && sha256sum get.sh >>SHA256SUMS)
(cd "$repo" && CGO_ENABLED=0 go build -o "$WORK/relserver" ./test/system/relserver) || fail "build relserver gagal"
docker cp "$WORK/relserver" "$NAME:/opt/relserver"
docker cp "$REL" "$NAME:/opt/release"
docker exec -d "$NAME" sh -c "/opt/relserver /opt/release 127.0.0.1:$REL_PORT $REL_REPO $REL_VERSION >/opt/relserver.log 2>&1"
rel_up=0
for _ in $(seq 20); do
	if in_box curl -fs -o /dev/null "http://127.0.0.1:$REL_PORT/$REL_REPO/releases/tag/v$REL_VERSION"; then
		rel_up=1
		break
	fi
	sleep 0.5
done
((rel_up)) || fail "server rilis lokal tidak menjawab: $(in_box cat /opt/relserver.log 2>&1)"
# rv runs the installed runova as root against the local release server.
rv() { in_box env "RUNOVA_GITHUB=http://127.0.0.1:$REL_PORT" "RUNOVA_REPO=$REL_REPO" /usr/local/bin/runova "$@"; }

[[ $(in_box /usr/local/bin/runova version) == "runova 0.0.2-systest" ]] || fail "versi awal skenario 7"
rc=0
out=$(rv update --check 2>&1) || rc=$?
[[ $rc == 10 && $out == *"$REL_VERSION"* ]] || fail "update --check: kode $rc, keluaran: $out"
rc=0
out=$(rv update 2>&1) || rc=$?
[[ $rc != 0 && $out == *--yes* ]] || fail "update tanpa terminal dan tanpa --yes harus ditolak: kode $rc, keluaran: $out"
[[ $(in_box /usr/local/bin/runova version) == "runova 0.0.2-systest" ]] || fail "update tanpa --yes mengubah binary"

out=$(rv update --yes 2>&1) || {
	printf '%s\n' "$out" >&2
	fail "runova update --yes gagal"
}
printf '%s\n' "$out" | tail -n 8 | sed 's/^/   | /'
[[ $out != *Password* ]] || fail "update mencetak password"
[[ $(in_box /usr/local/bin/runova version) == "runova $REL_VERSION" ]] || fail "versi setelah update: $(in_box /usr/local/bin/runova version)"
[[ $(in_box cat /var/lib/runova/rollback/VERSION) == 0.0.2-systest ]] || fail "rollback/VERSION setelah update"
rc=0
rv update --check >/dev/null 2>&1 || rc=$?
[[ $rc == 0 ]] || fail "update --check setelah update: kode $rc"
req "$ADMIN_JAR" POST /api/login "$(jq -nc --arg p "$ADMIN_PASSWORD" '{username: "admin", password: $p}')"
expect 200
[[ $(curl_ok "http://$IP:$IP_PORT/db") == rows=3 ]] || fail "data aplikasi hilang setelah update"
pass "update ke $REL_VERSION lewat get.sh rilis lokal; login dan data utuh; rollback/VERSION=0.0.2-systest"

rc=0
out=$(rv rollback 2>&1) || rc=$?
[[ $rc != 0 && $out == *--yes* ]] || fail "rollback tanpa terminal dan tanpa --yes harus ditolak: kode $rc, keluaran: $out"
out=$(rv rollback --yes 2>&1) || {
	printf '%s\n' "$out" >&2
	fail "runova rollback --yes gagal"
}
printf '%s\n' "$out" | tail -n 8 | sed 's/^/   | /'
[[ $(in_box /usr/local/bin/runova version) == "runova 0.0.2-systest" ]] || fail "versi setelah rollback: $(in_box /usr/local/bin/runova version)"
[[ $(in_box cat /var/lib/runova/rollback/VERSION) == "$REL_VERSION" ]] || fail "rollback/VERSION setelah rollback"
[[ $(in_box systemctl is-active runova) == active ]] || fail "runova tidak aktif setelah rollback"
as_runova check >/dev/null || fail "runova check gagal setelah rollback"
for _ in $(seq 60); do
	[[ $(curl -sk -m 3 -o /dev/null -w '%{http_code}' "$PANEL/api/me") =~ ^(200|401)$ ]] && break
	sleep 1
done
req "$ADMIN_JAR" POST /api/login "$(jq -nc --arg p "$ADMIN_PASSWORD" '{username: "admin", password: $p}')"
expect 200
req "$BUDI_JAR" POST /api/login "$(jq -nc --arg p "$BUDI_PASSWORD" '{username: "budi", password: $p}')"
expect 200
[[ $(curl_ok "http://$IP:$IP_PORT/db") == rows=3 ]] || fail "data aplikasi hilang setelah rollback"
wait_app "$BUDI_JAR" "$APP_ID"
pass "rollback kembali ke 0.0.2-systest; panel login, pengguna, database, dan aplikasi utuh"

printf '\nSemua 9 skenario lulus.\n'
