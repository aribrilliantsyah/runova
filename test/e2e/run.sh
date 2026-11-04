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
LOOP_PID=""

cleanup() {
	if [[ -n $LOOP_PID ]]; then
		kill "$LOOP_PID" 2>/dev/null || true
	fi
	rm -rf "$WORK"
}
trap cleanup EXIT

# shellcheck source=test/e2e/lib.sh
source "$here/lib.sh"

ADMIN_JAR="$WORK/admin.jar"
BUDI_JAR="$WORK/budi.jar"
ANDI_JAR="$WORK/andi.jar"
BUDI_PASSWORD="budi-password-123"
ANDI_PASSWORD="andi-password-456"
HELLO_V1=""
HELLO_V2=""
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

# ---------------------------------------------------------------------------
scenario "9 hosting-domain"
req "$ADMIN_JAR" GET /api/settings
expect 200
req "$ADMIN_JAR" PUT /api/settings "$(jq -c '.public_ip = "127.0.0.1"' <<<"$BODY")"
expect 200
# A new public IP changes the panel's IP-listener certificate; Caddy 2.6.2
# swaps it asynchronously and fails some TLS handshakes for about half a
# second (measured). Wait for five good handshakes in a row.
ok=0
for ((i = 0; i < 100 && ok < 5; i++)); do
	if curl -sk -m 2 -o /dev/null "$PANEL/api/me"; then ok=$((ok + 1)); else ok=0; fi
	sleep 0.1
done
((ok == 5)) || fail "panel tidak stabil setelah IP publik diganti"
req "$ADMIN_JAR" POST /api/hosting-domains '{"name":"*.apps.localhost"}'
expect 201
HOSTING_ID=$(jq -r .id <<<"$BODY")
[[ $(jq -r .name <<<"$BODY") == apps.localhost ]] || fail "nama domain hosting: $BODY"
req "$BUDI_JAR" GET /api/hosting-domains
expect 200
[[ $(jq -r '.domains | map(.name) | join(",")' <<<"$BODY") == apps.localhost ]] || fail "budi harus melihat apps.localhost: $BODY"

req "$BUDI_JAR" POST "/api/apps/$HELLO_ID/domains" '{"host":"budi.com"}'
expect 403
req "$BUDI_JAR" POST "/api/apps/$HELLO_ID/domains" "$(jq -nc --argjson h "$HOSTING_ID" '{hosting_domain_id: $h, label: "www"}')"
expect 400
req "$BUDI_JAR" POST "/api/apps/$HELLO_ID/domains" "$(jq -nc --argjson h "$HOSTING_ID" '{hosting_domain_id: $h, label: "cv-budi"}')"
expect 201

curl_sub() { curl -sk -m 10 --resolve "cv-budi.apps.localhost:443:127.0.0.1" "$@" "https://cv-budi.apps.localhost/"; }
hello=""
for ((i = 0; i < 60; i++)); do
	hello=$(curl_sub 2>/dev/null) && [[ $hello == "hello v2" ]] && break
	sleep 1
done
[[ $hello == "hello v2" ]] || fail "https://cv-budi.apps.localhost/ = '$hello' (diharapkan hello v2)"
issuer=$(echo | openssl s_client -connect 127.0.0.1:443 -servername cv-budi.apps.localhost 2>/dev/null | openssl x509 -noout -issuer 2>/dev/null) || true
[[ $issuer == *"Caddy Local Authority"* ]] || fail "penerbit sertifikat bukan CA internal Caddy: '$issuer'"

req "$ANDI_JAR" POST "/api/apps/$HELLO_ID/domains" "$(jq -nc --argjson h "$HOSTING_ID" '{hosting_domain_id: $h, label: "curang"}')"
expect 404
req "$ANDI_JAR" POST "/api/apps/$ANDI_APP/domains" "$(jq -nc --argjson h "$HOSTING_ID" '{hosting_domain_id: $h, label: "cv-budi"}')"
expect 409

req "$ADMIN_JAR" DELETE "/api/hosting-domains/$HOSTING_ID"
expect 409
req "$ADMIN_JAR" PATCH "/api/hosting-domains/$HOSTING_ID" '{"enabled":false}'
expect 200
off=""
for ((i = 0; i < 30; i++)); do
	off=$(curl_sub -o /dev/null -w '%{http_code}' 2>/dev/null) || off=000
	[[ $off != 200 ]] && break
	sleep 1
done
[[ $off != 200 ]] || fail "subdomain tetap menjawab 200 setelah domain hosting dimatikan"
req "$ADMIN_JAR" PATCH "/api/hosting-domains/$HOSTING_ID" '{"enabled":true}'
expect 200
back=""
for ((i = 0; i < 60; i++)); do
	back=$(curl_sub 2>/dev/null) && [[ $back == "hello v2" ]] && break
	sleep 1
done
[[ $back == "hello v2" ]] || fail "subdomain tidak kembali setelah domain hosting dinyalakan: '$back'"
pass "cv-budi.apps.localhost menjawab hello v2 lewat HTTPS (sertifikat Caddy Local Authority); domain sendiri 403, label www 400, andi 404/409; mati: $off, nyala: kembali"

printf '\nSemua 9 skenario lulus.\n'
