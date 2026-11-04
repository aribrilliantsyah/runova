#!/usr/bin/env bats
# Tests for get.sh against a local HTTP server laid out like GitHub releases:
#   /<repo>/releases/latest            -> 302 to /<repo>/releases/tag/v<latest>
#   /<repo>/releases/download/v<v>/... -> the release files
# The release holds a fake install.sh that records its arguments.

setup() {
	command -v python3 >/dev/null || skip "python3 is needed for the local release server"
	WEB="$BATS_TEST_TMPDIR/web"
	export OUT="$BATS_TEST_TMPDIR/install-args"
	make_release 0.1.0
	make_release 0.2.0
	PORT=$((20000 + RANDOM % 20000))
	python3 - "$WEB" "$PORT" >"$BATS_TEST_TMPDIR/server.log" 2>&1 <<'PY' &
import http.server, os, sys
root, port = sys.argv[1], int(sys.argv[2])
class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=root, **k)
    def do_HEAD(self): self.route() or super().do_HEAD()
    def do_GET(self): self.route() or super().do_GET()
    def route(self):
        if self.path.endswith("/releases/latest"):
            self.send_response(302)
            self.send_header("Location", self.path.replace("/latest", "/tag/v0.2.0"))
            self.end_headers()
            return True
        if "/releases/tag/" in self.path:
            self.send_response(200)
            self.end_headers()
            return True
        return False
http.server.ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
PY
	SERVER=$!
	for _ in $(seq 50); do curl -fs "http://127.0.0.1:$PORT/" >/dev/null 2>&1 && break; sleep 0.1; done
	export RUNOVA_GITHUB="http://127.0.0.1:$PORT" RUNOVA_REPO=me/runova
	GET="$BATS_TEST_DIRNAME/../get.sh"
}

teardown() {
	[ -n "${SERVER:-}" ] && kill "$SERVER" 2>/dev/null || true
}

# make_release builds the archive and SHA256SUMS of one version.
make_release() {
	local v=$1 name="runova-$1-linux-amd64"
	local stage="$BATS_TEST_TMPDIR/stage/$name" dir="$WEB/me/runova/releases/download/v$1"
	mkdir -p "$stage" "$dir"
	cat >"$stage/install.sh" <<EOF
#!/usr/bin/env bash
if [[ \$1 == --plan ]]; then echo "PLAN $v"; exit 0; fi
[[ -t 0 ]] && echo "installer stdin is a terminal" >&2
printf '%s|%s\n' "$v" "\$*" >"\$OUT"
EOF
	chmod +x "$stage/install.sh"
	echo binary >"$stage/runova"
	(cd "$stage" && sha256sum install.sh runova >SHA256SUMS)
	tar -czf "$dir/$name.tar.gz" -C "$BATS_TEST_TMPDIR/stage" "$name"
	(cd "$dir" && sha256sum "$name.tar.gz" >SHA256SUMS)
}

@test "installs the latest release and passes the options to install.sh" {
	run bash "$GET" --domain panel.contoh.id --yes
	[ "$status" -eq 0 ]
	[ "$(cat "$OUT")" = "0.2.0|--domain panel.contoh.id --yes" ]
}

@test "--version picks a release, with or without the v" {
	run bash "$GET" --version v0.1.0 --port 8443 --yes
	[ "$status" -eq 0 ]
	[ "$(cat "$OUT")" = "0.1.0|--port 8443 --yes" ]
	run bash "$GET" --version=0.1.0 -y
	[ "$status" -eq 0 ]
	[ "$(cat "$OUT")" = "0.1.0|-y" ]
}

@test "without --yes and without a terminal it refuses before installing" {
	run setsid bash "$GET" --port 8443
	[ "$status" -ne 0 ]
	[[ $output == *"No terminal to ask on"* ]]
	[ ! -e "$OUT" ]
}

@test "asks on the terminal, then runs the installer with --yes and no terminal on stdin" {
	command -v script >/dev/null || skip "script(1) is needed for a pseudo-terminal"
	# The answer goes to the pseudo-terminal, like a user typing it; stdin of
	# bash is the script itself, as under `curl | sudo bash`.
	run bash -c "{ sleep 1; printf 'y\n'; } | script -qec 'bash -s -- --port 8443 <\"$GET\"' /dev/null"
	[ "$status" -eq 0 ]
	[[ $output == *"PLAN 0.2.0"* ]]
	[[ $output == *"Continue? [y/N]"* ]]
	[[ $output != *"installer stdin is a terminal"* ]]
	[ "$(cat "$OUT")" = "0.2.0|--port 8443 --yes" ]
}

@test "answering no on the terminal installs nothing" {
	command -v script >/dev/null || skip "script(1) is needed for a pseudo-terminal"
	run bash -c "{ sleep 1; printf 'n\n'; } | script -qec 'bash -s -- --port 8443 <\"$GET\"' /dev/null"
	[ "$status" -ne 0 ]
	[[ $output == *"Canceled"* ]]
	[ ! -e "$OUT" ]
}

@test "works when piped into bash, like curl | bash" {
	run bash -c "bash -s -- --yes <'$GET'"
	[ "$status" -eq 0 ]
	[ "$(cat "$OUT")" = "0.2.0|--yes" ]
}

@test "a tampered archive is refused before anything runs" {
	echo x >>"$WEB/me/runova/releases/download/v0.2.0/runova-0.2.0-linux-amd64.tar.gz"
	run bash "$GET" --yes
	[ "$status" -ne 0 ]
	[[ $output == *"Archive checksum does not match"* ]]
	[ ! -e "$OUT" ]
}

@test "an unknown version names the URL it could not download" {
	run bash "$GET" --version 9.9.9
	[ "$status" -ne 0 ]
	[[ $output == *"releases/download/v9.9.9/runova-9.9.9-linux-amd64.tar.gz"* ]]
}

@test "an invalid version is refused" {
	run bash "$GET" --version latest
	[ "$status" -ne 0 ]
	[[ $output == *"Invalid version"* ]]
}

@test "leaves no temporary folder behind" {
	run bash "$GET" --yes
	[ "$status" -eq 0 ]
	[ -z "$(find /tmp -maxdepth 1 -name 'runova-get.*' -newer "$GET")" ]
}
