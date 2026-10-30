#!/usr/bin/env bash
# End-to-end scenarios against the stack from stack.sh. Stops at the first failure.
#
#   test/e2e/stack.sh up && test/e2e/run.sh && test/e2e/stack.sh down
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=test/e2e/env.sh
source "$here/env.sh"

PANEL="https://127.0.0.1:$PANEL_PORT"
WORK="$(mktemp -d)"
SCENARIO=""
UP=""
BODY=""
CODE=""
LOOP_PID=""

cleanup() {
	if [[ -n $LOOP_PID ]]; then
		kill "$LOOP_PID" 2>/dev/null || true
	fi
	rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
	printf '\nGAGAL [%s]: %s\n' "$SCENARIO" "$*" >&2
	if [[ -f $STATE/runova.log ]]; then
		printf -- '--- 20 baris terakhir log Runova ---\n' >&2
		tail -n 20 "$STATE/runova.log" >&2
	fi
	exit 1
}

scenario() {
	SCENARIO=$1
	printf '== %s\n' "$1"
}

pass() { printf '   ok: %s\n' "$*"; }

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

# expect CODE: fails unless the last request answered with CODE.
expect() {
	[[ $CODE == "$1" ]] || fail "status $CODE (diharapkan $1): $BODY"
}

# upload JAR FILE: sets UP (the upload id) and BODY; not for use in $(...), which would lose them.
upload() {
	local jar=$1 file=$2 out
	out=$(curl -sk -b "$jar" -c "$jar" -m 300 -w $'\n%{http_code}' -F "file=@$file" "$PANEL/api/uploads") || fail "unggah $file gagal"
	CODE=${out##*$'\n'}
	BODY=${out%$'\n'*}
	expect 201
	UP=$(jq -r .id <<<"$BODY")
}

# wait_app JAR ID: waits until the app runs and no deploy is in flight.
wait_app() {
	local jar=$1 id=$2 i status busy
	for ((i = 0; i < 180; i++)); do
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
	fail "aplikasi $id tidak running dalam 180 detik (status $status)"
}

# curl_ok URL [TRIES]: GET until 200 and print the body.
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

login() {
	local jar=$1 user=$2 pass=$3
	req "$jar" POST /api/login "$(jq -nc --arg u "$user" --arg p "$pass" '{username: $u, password: $p}')"
	expect 200
}

# build_hello VERSION: prints the path of a tar.gz holding the static sample binary and .env.example.
build_hello() {
	local v=$1 dir
	dir="$WORK/hello-$v"
	mkdir -p "$dir"
	(cd "$REPO" && CGO_ENABLED=0 go build -ldflags "-X main.version=$v" -o "$dir/hello" ./test/e2e/hello)
	printf 'PORT=8080\n' >"$dir/.env.example"
	tar -czf "$dir.tar.gz" -C "$dir" hello .env.example
	printf '%s\n' "$dir.tar.gz"
}

# build_spring: builds the Spring Boot sample once and caches it under .cache.
build_spring() {
	local jar="$CACHE/hello-spring.jar"
	if [[ ! -f $jar ]]; then
		mkdir -p "$CACHE/m2"
		echo "   membangun hello-spring di maven:3-eclipse-temurin-21 (pertama kali butuh unduhan)" >&2
		docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -e MAVEN_CONFIG=/tmp/.m2 \
			-v "$REPO/testdata/hello-spring:/src:ro" -v "$CACHE/m2:/m2" -v "$CACHE:/out" \
			maven:3-eclipse-temurin-21 \
			sh -c 'cp -r /src /tmp/build && cd /tmp/build && mvn -q -B -Dmaven.repo.local=/m2 -DskipTests package && cp target/*.jar /out/hello-spring.jar' >&2 ||
			fail "build hello-spring gagal"
	fi
	printf '%s\n' "$jar"
}

# create_app JAR UPLOAD NAME EXTRA_JSON: creates and waits for the app, sets APP_ID and IP_PORT.
create_app() {
	local jar=$1 up=$2 name=$3 extra=$4
	req "$jar" POST /api/apps "$(jq -nc --arg up "$up" --arg n "$name" --argjson extra "$extra" \
		'{upload_id: $up, name: $n, internal_port: 8080, memory_mb: 128, cpu_milli: 250, ip_access: true, env: []} + $extra')"
	expect 201
	APP_ID=$(jq -r .app.id <<<"$BODY")
	IP_PORT=$(jq -r .app.ip_port <<<"$BODY")
	wait_app "$jar" "$APP_ID"
}

# container_of USER APP: the running container's name.
container_of() {
	docker ps --filter "label=runova.app" --format '{{.Names}}' | grep -E "^runova-$1-$2-[0-9]+$" | head -n 1
}

ADMIN_JAR="$WORK/admin.jar"
BUDI_JAR="$WORK/budi.jar"
ANDI_JAR="$WORK/andi.jar"
BUDI_PASSWORD="budi-password-123"
ANDI_PASSWORD="andi-password-456"
HELLO_V1=""
HELLO_V2=""
APP_ID=""
IP_PORT=""
BUDI_DB=""
HELLO_ID=""
HELLO_PORT=""

HELLO_V1=$(build_hello v1)
HELLO_V2=$(build_hello v2)
SPRING_JAR=$(build_spring)

# ---------------------------------------------------------------------------
scenario "1 login"
out=$("$RUNOVA_BIN" admin create admin) || fail "runova admin create gagal (stack bekas pakai? jalankan 'test/e2e/stack.sh down' lalu 'up' lagi)"
ADMIN_PASSWORD=$(sed -n 's/^Password: //p' <<<"$out")
[[ -n $ADMIN_PASSWORD ]] || fail "password admin tidak tercetak: $out"
login "$ADMIN_JAR" admin "$ADMIN_PASSWORD"
req "$ADMIN_JAR" GET /api/me
expect 200
[[ $(jq -r .role <<<"$BODY") == admin ]] || fail "peran bukan admin: $BODY"
req "$ADMIN_JAR" GET /api/server
expect 200
for svc in docker caddy postgres; do
	[[ $(jq -r ".$svc.ok" <<<"$BODY") == true ]] || fail "halaman Server: $svc tidak sehat: $BODY"
done
pass "admin dibuat, login API, Docker/Caddy/PostgreSQL sehat"

# ---------------------------------------------------------------------------
scenario "2 user+db"
req "$ADMIN_JAR" POST /api/users "$(jq -nc --arg p "$BUDI_PASSWORD" '{username: "budi", password: $p, role: "user"}')"
expect 201
req "$ADMIN_JAR" POST /api/users "$(jq -nc --arg p "$ANDI_PASSWORD" '{username: "andi", password: $p, role: "user"}')"
expect 201
login "$BUDI_JAR" budi "$BUDI_PASSWORD"
login "$ANDI_JAR" andi "$ANDI_PASSWORD"
req "$BUDI_JAR" POST /api/databases '{"suffix":"app"}'
expect 201
BUDI_DB=$(jq -r .id <<<"$BODY")
[[ $(jq -r .name <<<"$BODY") == budi_app ]] || fail "nama database: $BODY"
req "$ANDI_JAR" POST /api/databases '{"suffix":"app"}'
expect 201
req "$BUDI_JAR" GET /api/databases
expect 200
[[ $(jq -r '.databases | map(.name) | join(",")' <<<"$BODY") == budi_app ]] || fail "budi harus hanya melihat budi_app: $BODY"
pass "budi dan andi dibuat; database budi_app dan andi_app ada"

# ---------------------------------------------------------------------------
scenario "3 deploy-binary"
upload "$BUDI_JAR" "$HELLO_V1"; up=$UP
[[ $(jq -r .report.kind <<<"$BODY") == static ]] || fail "laporan unggahan: $BODY"
create_app "$BUDI_JAR" "$up" hello "$(jq -nc --argjson db "$BUDI_DB" '{database_id: $db}')"
HELLO_ID=$APP_ID
HELLO_PORT=$IP_PORT
body=$(curl_ok "http://127.0.0.1:$HELLO_PORT/")
[[ $body == "hello v1" ]] || fail "GET / = $body"
body=$(curl_ok "http://127.0.0.1:$HELLO_PORT/db")
[[ $body == rows=* ]] || fail "GET /db = $body"
pass "hello v1 berjalan di 127.0.0.1:$HELLO_PORT dan terhubung ke budi_app ($body)"

# ---------------------------------------------------------------------------
scenario "4 deploy-jar"
upload "$BUDI_JAR" "$SPRING_JAR"; up=$UP
[[ $(jq -r .report.kind <<<"$BODY") == jar ]] || fail "laporan unggahan: $BODY"
req "$BUDI_JAR" POST /api/apps "$(jq -nc --arg up "$up" \
	'{upload_id: $up, name: "spring", java_version: 21, jvm_opts: "", internal_port: 8080, memory_mb: 512, cpu_milli: 500, ip_access: true,
	  env: [{name: "DB_URL", value: "jdbc:postgresql://host.docker.internal/none", secret: false}]}')"
expect 201
SPRING_ID=$(jq -r .app.id <<<"$BODY")
SPRING_PORT=$(jq -r .app.ip_port <<<"$BODY")
wait_app "$BUDI_JAR" "$SPRING_ID"
body=$(curl_ok "http://127.0.0.1:$SPRING_PORT/" 60)
[[ $body == "hello from jdbc:postgresql://host.docker.internal/none" ]] || fail "GET / = $body"
pass "Spring Boot di Java 21 menjawab 200 di 127.0.0.1:$SPRING_PORT"

# ---------------------------------------------------------------------------
scenario "5 zero-downtime"
upload "$BUDI_JAR" "$HELLO_V2"; up=$UP
: >"$WORK/loop.log"
(
	while [[ ! -e $WORK/loop.stop ]]; do
		# One line per request, "<body> <status>": the body's own newline sits before the status, so drop it.
		out=$(curl -s -m 3 -w ' %{http_code}' "http://127.0.0.1:$HELLO_PORT/" 2>/dev/null) || out=curl-error
		printf '%s\n' "${out//$'\n'/}" >>"$WORK/loop.log"
		sleep 0.05
	done
) &
LOOP_PID=$!
sleep 1
req "$BUDI_JAR" POST "/api/apps/$HELLO_ID/versions" "$(jq -nc --arg up "$up" '{upload_id: $up}')"
expect 201
wait_app "$BUDI_JAR" "$HELLO_ID"
for ((i = 0; i < 30; i++)); do
	[[ $(tail -n 1 "$WORK/loop.log") == "hello v2 200" ]] && break
	sleep 1
done
sleep 2
touch "$WORK/loop.stop"
wait "$LOOP_PID" || true
LOOP_PID=""
total=$(wc -l <"$WORK/loop.log")
bad=$(grep -vcE '^hello v[12] 200$' "$WORK/loop.log" || true)
v1=$(grep -c '^hello v1 200$' "$WORK/loop.log" || true)
v2=$(grep -c '^hello v2 200$' "$WORK/loop.log" || true)
((bad == 0)) || fail "$bad dari $total permintaan gagal selama deploy; contoh: $(grep -vE '^hello v[12] 200$' "$WORK/loop.log" | sort | uniq -c | head -n 3 | tr '\n' ';')"
((v1 > 0 && v2 > 0)) || fail "respons tidak beralih (v1=$v1, v2=$v2 dari $total)"
[[ $(tail -n 1 "$WORK/loop.log") == "hello v2 200" ]] || fail "respons terakhir bukan v2"
pass "$total permintaan, 0 gagal; v1=$v1 lalu v2=$v2"

# ---------------------------------------------------------------------------
scenario "6 isolation"
upload "$ANDI_JAR" "$HELLO_V1"; up=$UP
create_app "$ANDI_JAR" "$up" hello '{}'
ANDI_APP=$APP_ID
andi_container=$(container_of andi hello) || fail "container andi tidak ditemukan"
andi_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$andi_container")
[[ -n $andi_ip ]] || fail "IP container andi tidak ditemukan"

req "$BUDI_JAR" POST "/api/databases/$BUDI_DB/connection/reveal"
expect 200
pw=$(jq -r .password <<<"$BODY")
[[ -n $pw && $pw != null ]] || fail "password database budi kosong: $BODY"
# Controls first: budi's container can reach its own database and the host, so a refusal below means isolation.
own=$(curl_ok "http://127.0.0.1:$HELLO_PORT/connect?dsn=postgres%3A%2F%2Fbudi%3A${pw}%40host.docker.internal%3A$PG_PORT%2Fbudi_app")
[[ $own == ok ]] || fail "budi tidak bisa membuka budi_app dari containernya: $own"
host_dial=$(curl_ok "http://127.0.0.1:$HELLO_PORT/dial?addr=host.docker.internal:$PG_PORT")
[[ $host_dial == ok ]] || fail "container budi tidak bisa menjangkau host: $host_dial"

other=$(curl_ok "http://127.0.0.1:$HELLO_PORT/connect?dsn=postgres%3A%2F%2Fbudi%3A${pw}%40host.docker.internal%3A$PG_PORT%2Fandi_app")
[[ $other == error* && $other == *"permission denied"* ]] || fail "budi tidak ditolak di andi_app: $other"
cross=$(curl_ok "http://127.0.0.1:$HELLO_PORT/dial?addr=$andi_ip:8080")
[[ $cross == error* ]] || fail "container budi menjangkau container andi ($andi_ip): $cross"
req "$BUDI_JAR" GET "/api/apps/$ANDI_APP"
expect 404
pass "andi_app menolak budi (permission denied); $andi_ip tidak terjangkau dari jaringan budi; app andi 404 bagi budi"

# ---------------------------------------------------------------------------
scenario "7 restart-runova"
"$here/stack.sh" restart-runova >/dev/null || fail "Runova tidak naik lagi"
body=$(curl_ok "http://127.0.0.1:$HELLO_PORT/" 30)
[[ $body == "hello v2" ]] || fail "setelah restart GET / = $body"
for id in "$HELLO_ID" "$SPRING_ID"; do
	req "$BUDI_JAR" GET "/api/apps/$id"
	expect 200
	[[ $(jq -r .app.status <<<"$BODY") == running ]] || fail "status app $id setelah restart: $BODY"
done
curl_ok "http://127.0.0.1:$SPRING_PORT/" 30 >/dev/null
pass "hello dan Spring tetap 200 dan 'running' setelah Runova dimulai ulang; sesi tetap berlaku"

# ---------------------------------------------------------------------------
scenario "8 backup"
for _ in 1 2 3; do
	body=$(curl_ok "http://127.0.0.1:$HELLO_PORT/db?add=1")
done
[[ $body == rows=3 ]] || fail "tiga baris diharapkan, dapat: $body"
req "$BUDI_JAR" POST "/api/databases/$BUDI_DB/backups"
expect 201
backup=$(jq -r .id <<<"$BODY")
docker exec "$PG_CONTAINER" psql -X -q -v ON_ERROR_STOP=1 -U postgres -p "$PG_PORT" -d budi_app -c 'DELETE FROM visits' >/dev/null
[[ $(curl_ok "http://127.0.0.1:$HELLO_PORT/db") == rows=0 ]] || fail "baris tidak terhapus"
req "$BUDI_JAR" POST "/api/databases/$BUDI_DB/backups/$backup/restore" '{"confirm":"salah"}'
expect 400
req "$BUDI_JAR" POST "/api/databases/$BUDI_DB/backups/$backup/restore" '{"confirm":"budi_app"}'
expect 200
body=$(curl_ok "http://127.0.0.1:$HELLO_PORT/db")
[[ $body == rows=3 ]] || fail "setelah restore: $body"
pass "backup $backup; 3 baris dihapus; restore mengembalikan 3 baris"

printf '\nSemua 8 skenario lulus.\n'
