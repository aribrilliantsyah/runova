# shellcheck shell=bash disable=SC2034
# Helpers shared by run.sh and nginx.sh. The sourcing script has sourced env.sh
# and set WORK (a scratch folder) and PANEL (the panel's base URL) first.

SCENARIO=""
UP=""
BODY=""
CODE=""
APP_ID=""
IP_PORT=""

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
	local args=(-sSk -X "$method" -b "$jar" -c "$jar" -m 120 -w $'\n%{http_code}')
	if [[ -n $data ]]; then
		args+=(-H 'Content-Type: application/json' --data "$data")
	fi
	out=$(curl "${args[@]}" "$PANEL$path" 2>&1) || fail "curl $method $path gagal (exit $?): $out"
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
