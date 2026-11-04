#!/usr/bin/env bash
# nginx in front of Runova (mode A), as in docs/nginx-front-proxy.md. Needs the
# stack from stack.sh (a fresh one, or one that run.sh has used). nginx 1.18
# (ubuntu:22.04) runs in a container with docs/examples/nginx-runova.conf, the
# file the guides embed, with only the addresses and names replaced. Two client
# containers with their own IPs on the same Docker network play the visitors.
#
#   test/e2e/stack.sh up && test/e2e/nginx.sh && test/e2e/stack.sh down
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=test/e2e/env.sh
source "$here/env.sh"

PANEL="https://127.0.0.1:$PANEL_PORT"
WORK="$(mktemp -d)"
# shellcheck source=test/e2e/lib.sh
source "$here/lib.sh"

IMAGE=runova-e2e-nginx
NET=runova-e2e-nginx-net
SUBNET=172.29.77.0/24
GATEWAY=172.29.77.1 # the host, where the stack's Caddy listens on 80 and 443
NGINX_IP=172.29.77.2
CLIENT_A_IP=172.29.77.11
CLIENT_B_IP=172.29.77.12
NGINX=runova-e2e-nginx
CLIENT_A=runova-e2e-client-a
CLIENT_B=runova-e2e-client-b
PANEL_NAME=panel.localhost
# Not under .localhost: Caddy issues *.localhost names from its internal CA on
# its own, which hides what happens to a real name. Caddy behind nginx can never
# answer a public CA's challenge (and this lab is not reachable anyway).
HOSTING_NAME=app.runova-lab.test
SUB_LABEL=startboom
SUB_NAME=$SUB_LABEL.$HOSTING_NAME
CONF="$REPO/docs/examples/nginx-runova.conf"
CLIENT_PASSWORD_WRONG="salah-salah-salah"

cleanup() {
	docker rm -f "$NGINX" "$CLIENT_A" "$CLIENT_B" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT

command -v docker >/dev/null || fail "docker tidak ditemukan"
command -v openssl >/dev/null || fail "openssl tidak ditemukan"
command -v jq >/dev/null || fail "jq tidak ditemukan"
[[ $(curl -sk -o /dev/null -w "%{http_code}" "$PANEL/api/me") == 401 ]] || fail "stack belum berjalan (jalankan test/e2e/stack.sh up)"

# ---------------------------------------------------------------------------
scenario "0 guides embed the tested file"
for doc in nginx-front-proxy.md nginx-front-proxy.id.md; do
	embedded=$(awk '/<!-- nginx-runova.conf -->/ {found = 1; next} found && /^```nginx$/ {inside = 1; next} inside && /^```$/ {exit} inside {print}' "$REPO/docs/$doc")
	[[ -n $embedded ]] || fail "docs/$doc tidak memuat konfigurasi nginx setelah penanda"
	[[ $embedded == "$(cat "$CONF")" ]] || fail "konfigurasi di docs/$doc berbeda dari docs/examples/nginx-runova.conf"
done
pass "kedua panduan memuat docs/examples/nginx-runova.conf persis"

# ---------------------------------------------------------------------------
scenario "1 lab"
docker image inspect "$IMAGE" >/dev/null 2>&1 || docker build -q -t "$IMAGE" - >/dev/null <<'DOCKERFILE'
FROM ubuntu:22.04
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nginx curl openssl ca-certificates \
    && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled/default
DOCKERFILE
version=$(docker run --rm "$IMAGE" nginx -v 2>&1)
[[ $version == *"nginx/1.18"* ]] || fail "nginx 1.18 diharapkan, dapat: $version"

# Self-signed certificates under the paths the file expects (certbot's layout).
mkdir -p "$WORK/certs"
make_cert() {
	local name=$1 san=$2
	mkdir -p "$WORK/certs/$name"
	openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj "/CN=$name" -addext "subjectAltName=$san" \
		-keyout "$WORK/certs/$name/privkey.pem" -out "$WORK/certs/$name/fullchain.pem" 2>/dev/null
}
make_cert "$PANEL_NAME" "DNS:$PANEL_NAME"
make_cert "$HOSTING_NAME" "DNS:$HOSTING_NAME,DNS:*.$HOSTING_NAME"
sed -e "s/192\.168\.1\.10/$GATEWAY/g" -e "s/runova\.prodak\.id/$PANEL_NAME/g" -e "s/porto\.devkriyanesia\.my\.id/$HOSTING_NAME/g" "$CONF" >"$WORK/runova.conf"
chmod -R a+rX "$WORK"

docker network create --subnet "$SUBNET" --gateway "$GATEWAY" "$NET" >/dev/null
docker run -d --name "$NGINX" --network "$NET" --ip "$NGINX_IP" \
	-v "$WORK/runova.conf:/etc/nginx/conf.d/runova.conf:ro" -v "$WORK/certs:/etc/letsencrypt/live:ro" \
	"$IMAGE" nginx -g 'daemon off;' >/dev/null
docker run -d --name "$CLIENT_A" --network "$NET" --ip "$CLIENT_A_IP" "$IMAGE" sleep infinity >/dev/null
docker run -d --name "$CLIENT_B" --network "$NET" --ip "$CLIENT_B_IP" "$IMAGE" sleep infinity >/dev/null
docker exec "$NGINX" nginx -t >/dev/null 2>&1 || fail "nginx -t gagal: $(docker exec "$NGINX" nginx -t 2>&1)"
pass "$version di $NGINX_IP; klien A $CLIENT_A_IP, klien B $CLIENT_B_IP; Caddy di $GATEWAY"

# client NAME curl-args...: curl from inside a client container; names resolve to nginx.
client() {
	local who=$1
	shift
	docker exec "$who" curl -sk -m 20 \
		--resolve "$PANEL_NAME:443:$NGINX_IP" --resolve "$PANEL_NAME:80:$NGINX_IP" \
		--resolve "$SUB_NAME:443:$NGINX_IP" "$@"
}

# cjson WHO JARNAME METHOD PATH JSON: sets CODE and BODY through nginx, with a cookie jar inside the client.
cjson() {
	local who=$1 jar=$2 method=$3 path=$4 data=$5 out
	out=$(client "$who" -X "$method" -b "/tmp/$jar" -c "/tmp/$jar" -H 'Content-Type: application/json' \
		${6:+-H "$6"} --data "$data" -w $'\n%{http_code}' "https://$PANEL_NAME$path") || fail "curl $path dari $who gagal"
	CODE=${out##*$'\n'}
	BODY=${out%$'\n'*}
}

login_body() { jq -nc --arg p "$1" '{username: "admin", password: $p}'; }

# ---------------------------------------------------------------------------
scenario "2 runova setup"
out=$("$RUNOVA_BIN" admin create admin 2>/dev/null) || out=$("$RUNOVA_BIN" admin reset-password admin) || fail "admin tidak bisa dibuat atau direset"
ADMIN_PASSWORD=$(sed -n 's/^Password: //p' <<<"$out")
[[ -n $ADMIN_PASSWORD ]] || fail "password admin tidak tercetak: $out"
ADMIN_JAR="$WORK/admin.jar"
login "$ADMIN_JAR" admin "$ADMIN_PASSWORD"
req "$ADMIN_JAR" GET /api/settings
expect 200
req "$ADMIN_JAR" PUT /api/settings "$(jq -c --arg d "$PANEL_NAME" --arg ip "$NGINX_IP" '.panel_domain = $d | .trusted_proxies = [$ip]' <<<"$BODY")"
expect 200
req "$ADMIN_JAR" POST /api/hosting-domains "$(jq -nc --arg n "$HOSTING_NAME" '{name: $n}')"
expect 201
HOSTING_ID=$(jq -r .id <<<"$BODY")
HELLO=$(build_hello nginx)
upload "$ADMIN_JAR" "$HELLO"
create_app "$ADMIN_JAR" "$UP" hello-lab '{}'
req "$ADMIN_JAR" POST "/api/apps/$APP_ID/domains" "$(jq -nc --argjson h "$HOSTING_ID" --arg l "$SUB_LABEL" '{hosting_domain_id: $h, label: $l}')"
expect 201
DOMAIN_ID=$(jq -r .id <<<"$BODY")
pass "domain panel $PANEL_NAME, proxy tepercaya $NGINX_IP, domain hosting $HOSTING_NAME, aplikasi hello-lab (id $APP_ID)"

# ---------------------------------------------------------------------------
scenario "3 panel through nginx"
# Caddy issues the certificates of the new names on first use.
code=""
for ((i = 0; i < 60; i++)); do
	code=$(client "$CLIENT_A" -o /dev/null -w '%{http_code}' "https://$PANEL_NAME/") || code=000
	[[ $code == 200 ]] && break
	sleep 1
done
[[ $code == 200 ]] || fail "https://$PANEL_NAME/ lewat nginx = $code (diharapkan 200)"
result=$(client "$CLIENT_A" -L --max-redirs 5 -o /dev/null -w '%{http_code} %{num_redirects}' "https://$PANEL_NAME/")
[[ $result == "200 0" ]] || fail "panel lewat nginx: '$result' (diharapkan '200 0': tanpa pengalihan)"
pass "panel 200 tanpa pengalihan"

scenario "4 http to https once"
first=$(client "$CLIENT_A" -o /dev/null -w '%{http_code} %{redirect_url}' "http://$PANEL_NAME/")
[[ $first == "308 https://$PANEL_NAME/" ]] || fail "HTTP pertama: '$first'"
result=$(client "$CLIENT_A" -L --max-redirs 5 -o /dev/null -w '%{http_code} %{num_redirects}' "http://$PANEL_NAME/")
[[ $result == "200 1" ]] || fail "HTTP lalu HTTPS: '$result' (diharapkan '200 1': satu pengalihan)"
pass "http:// dialihkan satu kali ke https:// lalu 200"

# ---------------------------------------------------------------------------
scenario "5 login limit per real client"
for i in 1 2 3 4 5; do
	cjson "$CLIENT_A" a.jar POST /api/login "$(login_body "$CLIENT_PASSWORD_WRONG")"
	[[ $CODE == 401 ]] || fail "percobaan salah $i dari klien A: $CODE $BODY"
done
cjson "$CLIENT_A" a.jar POST /api/login "$(login_body "$ADMIN_PASSWORD")"
[[ $CODE == 429 ]] || fail "klien A harus terkunci setelah 5 kegagalan, dapat $CODE $BODY"
cjson "$CLIENT_B" b.jar POST /api/login "$(login_body "$ADMIN_PASSWORD")"
[[ $CODE == 200 ]] || fail "klien B (IP lain) harus tetap bisa masuk, dapat $CODE $BODY"
pass "A terkunci (429), B masuk (200) dengan username yang sama"

scenario "6 forged X-Forwarded-For"
for forged in 198.51.100.77 "198.51.100.78, 198.51.100.79" "$CLIENT_B_IP" "$NGINX_IP"; do
	cjson "$CLIENT_A" a.jar POST /api/login "$(login_body "$ADMIN_PASSWORD")" "X-Forwarded-For: $forged"
	[[ $CODE == 429 ]] || fail "klien A memalsukan X-Forwarded-For '$forged' dan lolos: $CODE $BODY"
done
pass "klien A tetap terkunci walau memalsukan X-Forwarded-For"

# ---------------------------------------------------------------------------
scenario "7 websocket"
# curl cannot speak WebSocket frames, but the upgrade must pass nginx and the
# first frames carry the app's log lines.
ws=$(client "$CLIENT_B" -i -N -m 5 -b /tmp/b.jar \
	-H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
	-H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' -H "Origin: https://$PANEL_NAME" \
	"https://$PANEL_NAME/api/apps/$APP_ID/logs?tail=20" 2>/dev/null || true)
[[ $ws == HTTP/1.1\ 101* ]] || fail "WebSocket lewat nginx tidak beralih (101): $(head -n 1 <<<"$ws")"
grep -aq 'listening' <<<"$ws" || fail "WebSocket tidak mengirim baris log aplikasi"
pass "WebSocket log: 101 Switching Protocols dan baris log mengalir"

# ---------------------------------------------------------------------------
scenario "8 new hosting subdomain"
sub=""
for ((i = 0; i < 60; i++)); do
	sub=$(client "$CLIENT_A" "https://$SUB_NAME/" 2>/dev/null) && [[ $sub == "hello nginx" ]] && break
	sleep 1
done
if [[ $sub != "hello nginx" ]]; then
	code=$(client "$CLIENT_A" -o /dev/null -w '%{http_code}' "https://$SUB_NAME/") || code=000
	req "$ADMIN_JAR" POST "/api/apps/$APP_ID/domains/$DOMAIN_ID/check"
	fail "https://$SUB_NAME/ lewat nginx = $code (diharapkan 200 hello nginx); status domain: $(jq -c '{status, detail}' <<<"$BODY")"
fi
issuer=$(docker exec "$NGINX" sh -c "echo | openssl s_client -connect $GATEWAY:443 -servername $SUB_NAME 2>/dev/null | openssl x509 -noout -issuer" 2>/dev/null) || true
[[ $issuer == *"Caddy Local Authority"* ]] || fail "sertifikat Caddy untuk $SUB_NAME: '$issuer' (diharapkan CA internal Caddy)"
req "$ADMIN_JAR" POST "/api/apps/$APP_ID/domains/$DOMAIN_ID/check"
expect 200
[[ $(jq -r .status <<<"$BODY") == active ]] || fail "status domain $SUB_NAME: $BODY (diharapkan active)"
pass "$SUB_NAME menjawab hello nginx lewat sertifikat wildcard nginx; Caddy memakai CA internal; status domain active"

# ---------------------------------------------------------------------------
scenario "9 activity records the client IP"
req "$ADMIN_JAR" GET "/api/activity?limit=200"
expect 200
failed_a=$(jq -r --arg ip "$CLIENT_A_IP" '[.[] | select(.action == "login_failed" and .ip == $ip)] | length' <<<"$BODY")
login_b=$(jq -r --arg ip "$CLIENT_B_IP" '[.[] | select(.action == "login" and .ip == $ip)] | length' <<<"$BODY")
via_nginx=$(jq -r --arg ip "$NGINX_IP" '[.[] | select(.ip == $ip)] | length' <<<"$BODY")
[[ $failed_a == 5 ]] || fail "login_failed dari klien A: $failed_a (diharapkan 5): $BODY"
[[ $login_b -ge 1 ]] || fail "login klien B tidak tercatat dengan IP-nya: $BODY"
[[ $via_nginx == 0 ]] || fail "$via_nginx entri aktivitas memakai IP nginx"
pass "5 login_failed dengan IP klien A, login klien B dengan IP-nya, tidak ada IP nginx"

printf '\nSemua skenario nginx lulus.\n'
