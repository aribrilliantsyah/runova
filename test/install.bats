#!/usr/bin/env bats
# Tests for install.sh. Every external command is a stub from test/fixtures/bin
# that appends its call to $CALLS; paths live under a scratch RUNOVA_ROOT.
# jq is needed for the daemon.json merge: see test/bats.sh.

setup() {
	FIXTURES="$BATS_TEST_DIRNAME/fixtures"
	export FIXTURES
	export RUNOVA_ROOT="$BATS_TEST_TMPDIR/root"
	export CALLS="$BATS_TEST_TMPDIR/calls"
	export RUNOVA_SRC="$BATS_TEST_TMPDIR/src"
	export PATH="$FIXTURES/bin:$PATH"
	: >"$CALLS"

	mkdir -p "$RUNOVA_SRC" "$RUNOVA_ROOT/etc/caddy" "$RUNOVA_ROOT/etc/postgresql/18/main"
	cp "$FIXTURES/src/runova" "$RUNOVA_SRC/runova"
	cp "$BATS_TEST_DIRNAME/../packaging/runova.service" "$RUNOVA_SRC/runova.service"
	cp "$FIXTURES/Caddyfile.pristine" "$RUNOVA_ROOT/etc/caddy/Caddyfile"
	printf 'local all postgres peer\nhost all all 127.0.0.1/32 scram-sha-256\n' >"$RUNOVA_ROOT/etc/postgresql/18/main/pg_hba.conf"
	os_release 26.04

	# shellcheck source=../install.sh
	source "$BATS_TEST_DIRNAME/../install.sh" --lib
}

os_release() {
	printf 'ID=ubuntu\nVERSION_ID="%s"\n' "$1" >"$RUNOVA_ROOT/etc/os-release"
}

# prepare does what the steps before the one under test leave behind.
prepare() {
	step_user_dirs >/dev/null
	step_binary >/dev/null
}

run_all_steps() {
	parse_args --yes
	run_steps
	summary
}

line_of() { grep -n -m1 -- "$1" "$CALLS" | cut -d: -f1; }

@test "rejects non-ubuntu-26.04" {
	os_release 24.04
	run check_os
	[ "$status" -ne 0 ]
	[[ "$output" == *"Ubuntu Server 26.04"* ]]
}

@test "accepts ubuntu 26.04 on x86_64" {
	run check_os
	[ "$status" -eq 0 ]
}

@test "rejects other architectures" {
	UNAME_M=aarch64 run check_os
	[ "$status" -ne 0 ]
	[[ "$output" == *"amd64"* ]]
}

@test "refuses modified Caddyfile" {
	echo 'x.co { }' >"$RUNOVA_ROOT/etc/caddy/Caddyfile"
	run step_caddy
	[ "$status" -ne 0 ]
	[[ "$output" == *"--yes"* ]]
	[[ "$output" == *"$RUNOVA_ROOT/etc/caddy/Caddyfile"* ]]
	[ "$(cat "$RUNOVA_ROOT/etc/caddy/Caddyfile")" = 'x.co { }' ]
	! grep -q systemctl "$CALLS"
}

@test "modified Caddyfile is backed up with --yes" {
	echo 'x.co { }' >"$RUNOVA_ROOT/etc/caddy/Caddyfile"
	parse_args --yes
	run step_caddy
	[ "$status" -eq 0 ]
	[ "$(cat "$RUNOVA_ROOT/etc/caddy/Caddyfile.runova-backup")" = 'x.co { }' ]
	grep -q 'admin localhost:2019' "$RUNOVA_ROOT/etc/caddy/Caddyfile"
}

@test "stock Caddyfile is replaced and caddy resumes its API config" {
	step_caddy
	[ "$(tr -d '\t\n ' <"$RUNOVA_ROOT/etc/caddy/Caddyfile")" = '{adminlocalhost:2019}' ]
	grep -q -- '--resume' "$RUNOVA_ROOT/etc/systemd/system/caddy.service.d/runova.conf"
	grep -q 'systemctl restart caddy' "$CALLS"
}

@test "caddy is restarted only when something changed" {
	step_caddy
	step_caddy
	[ "$(grep -c 'systemctl restart caddy' "$CALLS")" -eq 1 ]
}

@test "ssh port detected" {
	export SSHD_PORT=2222
	step_firewall
	grep -q "ufw allow 2222/tcp" "$CALLS"
	[ "$(line_of 'ufw allow 2222/tcp')" -lt "$(line_of 'ufw --force enable')" ]
	grep -q "ufw allow 20000:20999/tcp" "$CALLS"
	grep -q "ufw allow 5432/tcp" "$CALLS"
}

@test "ufw is not re-enabled when already active" {
	touch "$RUNOVA_ROOT/.ufw-active"
	step_firewall
	! grep -q "ufw --force enable" "$CALLS"
}

@test "panel port is opened in IP mode" {
	parse_args --port 9443
	step_firewall
	grep -q "ufw allow 9443/tcp" "$CALLS"
}

@test "--skip-firewall leaves ufw alone" {
	parse_args --yes --skip-firewall
	run step_firewall
	[ "$status" -eq 0 ]
	[[ "$output" == *"--skip-firewall"* ]]
	! grep -q ufw "$CALLS"
}

@test "ufw is used again when --skip-firewall is not given" {
	parse_args --yes --skip-firewall
	parse_args --yes
	step_firewall
	grep -q "ufw allow 80/tcp" "$CALLS"
}

@test "daemon.json merged" {
	mkdir -p "$RUNOVA_ROOT/etc/docker"
	echo '{"log-driver":"local"}' >"$RUNOVA_ROOT/etc/docker/daemon.json"
	step_docker_daemon
	jq -e '."log-driver"=="local" and ."default-address-pools"[0].base=="172.31.0.0/16" and ."default-address-pools"[0].size==24' "$RUNOVA_ROOT/etc/docker/daemon.json"
	grep -q 'systemctl restart docker' "$CALLS"
}

@test "daemon.json created when missing and left alone on rerun" {
	step_docker_daemon
	jq -e '."default-address-pools"[0].base=="172.31.0.0/16"' "$RUNOVA_ROOT/etc/docker/daemon.json"
	step_docker_daemon
	[ "$(grep -c 'systemctl restart docker' "$CALLS")" -eq 1 ]
}

@test "daemon.json with its own address pools is kept" {
	mkdir -p "$RUNOVA_ROOT/etc/docker"
	echo '{"default-address-pools":[{"base":"10.200.0.0/16","size":24}]}' >"$RUNOVA_ROOT/etc/docker/daemon.json"
	run step_docker_daemon
	[ "$status" -eq 0 ]
	[[ "$output" == *"default-address-pools"* ]]
	jq -e '."default-address-pools"[0].base=="10.200.0.0/16"' "$RUNOVA_ROOT/etc/docker/daemon.json"
}

@test "broken daemon.json stops the installer" {
	mkdir -p "$RUNOVA_ROOT/etc/docker"
	echo '{not json' >"$RUNOVA_ROOT/etc/docker/daemon.json"
	run step_docker_daemon
	[ "$status" -ne 0 ]
	[ "$(cat "$RUNOVA_ROOT/etc/docker/daemon.json")" = '{not json' ]
}

@test "pg_hba include added once" {
	step_postgres
	step_postgres
	[ "$(grep -c "^include_dir pg_hba.d$" "$RUNOVA_ROOT/etc/postgresql/18/main/pg_hba.conf")" -eq 1 ]
	! grep -q "include_dir '" "$RUNOVA_ROOT/etc/postgresql/18/main/pg_hba.conf"
}

@test "postgres listens on all interfaces, role and grant are created" {
	step_postgres
	grep -q "listen_addresses = '\*'" "$RUNOVA_ROOT/etc/postgresql/18/main/conf.d/runova.conf"
	grep -q 'CREATE ROLE runova LOGIN CREATEDB CREATEROLE' "$RUNOVA_ROOT/.psql-input"
	grep -q 'GRANT EXECUTE ON FUNCTION pg_reload_conf() TO runova' "$RUNOVA_ROOT/.psql-input"
	grep -q '172.30.0.0/16 scram-sha-256' "$RUNOVA_ROOT/etc/postgresql/18/main/pg_hba.d/runova.conf"
	grep -q 'chown postgres:runova' "$CALLS"
}

@test "pg_hba.d/runova.conf written by Runova is not overwritten on rerun" {
	step_postgres
	echo 'host all all 10.0.0.0/8 scram-sha-256' >>"$RUNOVA_ROOT/etc/postgresql/18/main/pg_hba.d/runova.conf"
	step_postgres
	grep -q '10.0.0.0/8' "$RUNOVA_ROOT/etc/postgresql/18/main/pg_hba.d/runova.conf"
}

@test "postgres restarts only when listen_addresses changed" {
	step_postgres
	step_postgres
	[ "$(grep -c 'systemctl restart postgresql' "$CALLS")" -eq 1 ]
}

@test "secret kept on rerun" {
	prepare
	step_secret
	local sum1
	sum1=$(sha256sum "$RUNOVA_ROOT/etc/runova/secret.key")
	step_secret
	[ "$sum1" = "$(sha256sum "$RUNOVA_ROOT/etc/runova/secret.key")" ]
	[ "$(grep -c keygen "$CALLS")" -eq 1 ]
}

@test "admin created only once" {
	prepare
	step_admin
	step_admin
	[ "$(grep -c 'admin create' "$CALLS")" -eq 1 ]
}

@test "admin password is captured" {
	prepare
	step_admin
	[ "$ADMIN_PASSWORD" = "Pw12345678abcdefghjk" ]
}

@test "runova subcommands run as the runova user, in folders it owns" {
	prepare
	step_secret
	step_admin
	for sub in keygen 'admin exists' 'admin create'; do
		local n
		n=$(line_of "runova $sub")
		[ "$(sed -n "$((n - 1))p" "$CALLS")" = "runuser runova" ]
	done
	[ "$(line_of 'chown runova:runova')" -lt "$(line_of 'runova keygen')" ]
}

@test "panel defaults to IP mode on port 8443" {
	prepare
	parse_args --yes
	step_panel
	grep -q 'runova panel set --port 8443' "$CALLS"
}

@test "panel domain and public ip are passed to runova" {
	prepare
	parse_args --yes --domain Panel.Contoh.ID --public-ip 203.0.113.5
	step_panel
	grep -q 'runova panel set --domain panel.contoh.id --public-ip 203.0.113.5' "$CALLS"
}

@test "trusted proxies are passed to runova, one flag each" {
	prepare
	parse_args --yes --port 8443 --trusted-proxy 10.0.0.5 --trusted-proxy=192.168.0.0/24 --trusted-proxy 2001:db8::/32
	step_panel
	grep -q 'runova panel set --port 8443 --trusted-proxy 10.0.0.5 --trusted-proxy 192.168.0.0/24 --trusted-proxy 2001:db8::/32' "$CALLS"
}

@test "parse_args rejects bad trusted proxies" {
	for bad in abc 10.0.0.0/0 ::/0 10.0.0.0/33 10.0.0.1/ 10.0.0.1/x 2001:db8::/129 999.1.1.1; do
		run parse_args --trusted-proxy "$bad"
		[ "$status" -ne 0 ]
	done
	run parse_args --trusted-proxy
	[ "$status" -ne 0 ]
}

@test "rerun without options keeps the panel address" {
	prepare
	step_admin
	parse_args --yes
	step_panel
	! grep -q 'panel set' "$CALLS"
}

@test "service unit is installed and restarted" {
	prepare
	step_env_file
	step_service
	cmp "$RUNOVA_SRC/runova.service" "$RUNOVA_ROOT/etc/systemd/system/runova.service"
	grep -q 'systemctl enable runova' "$CALLS"
	grep -q 'systemctl restart runova' "$CALLS"
}

@test "env file is written once and keeps admin edits" {
	prepare
	step_env_file
	grep -q '^RUNOVA_LISTEN=127.0.0.1:7070$' "$RUNOVA_ROOT/etc/runova/runova.env"
	echo 'RUNOVA_LISTEN=127.0.0.1:9000' >"$RUNOVA_ROOT/etc/runova/runova.env"
	step_env_file
	[ "$(cat "$RUNOVA_ROOT/etc/runova/runova.env")" = 'RUNOVA_LISTEN=127.0.0.1:9000' ]
}

@test "binary replaced atomically, no temporary file left" {
	prepare
	echo '#!/bin/sh' >"$RUNOVA_SRC/runova"
	echo 'echo "runova 9.9.9"' >>"$RUNOVA_SRC/runova"
	step_binary
	[ "$("$RUNOVA_ROOT/usr/local/bin/runova" version)" = "runova 9.9.9" ]
	[ -z "$(ls "$RUNOVA_ROOT"/usr/local/bin | grep -v '^runova$' || true)" ]
}

# set_src_version makes the release's binary report version $1.
set_src_version() {
	printf '#!/bin/sh\necho "runova %s"\n' "$1" >"$RUNOVA_SRC/runova"
}

@test "first install creates no rollback folder" {
	prepare
	[ ! -e "$RUNOVA_ROOT/var/lib/runova/rollback" ]
}

@test "an update keeps the old binary and its version in rollback/" {
	prepare
	set_src_version 0.1.0
	step_binary
	set_src_version 0.2.0
	step_binary
	local dir="$RUNOVA_ROOT/var/lib/runova/rollback"
	[ "$("$dir/runova" version)" = "runova 0.1.0" ]
	[ "$(cat "$dir/VERSION")" = "0.1.0" ]
	[ "$(stat -c %a "$dir/runova")" = 755 ]
	[ "$("$RUNOVA_ROOT/usr/local/bin/runova" version)" = "runova 0.2.0" ]
	[ -z "$(ls "$dir" | grep -v -e '^runova$' -e '^VERSION$' || true)" ]
}

@test "reinstalling the same version leaves rollback/ alone" {
	prepare
	set_src_version 0.1.0
	step_binary
	set_src_version 0.2.0
	step_binary
	step_binary
	local dir="$RUNOVA_ROOT/var/lib/runova/rollback"
	[ "$("$dir/runova" version)" = "runova 0.1.0" ]
	[ "$(cat "$dir/VERSION")" = "0.1.0" ]
}

@test "the next update replaces rollback/ with the version just left" {
	prepare
	set_src_version 0.1.0
	step_binary
	set_src_version 0.2.0
	step_binary
	set_src_version 0.3.0
	step_binary
	local dir="$RUNOVA_ROOT/var/lib/runova/rollback"
	[ "$("$dir/runova" version)" = "runova 0.2.0" ]
	[ "$(cat "$dir/VERSION")" = "0.2.0" ]
}

@test "an installed binary that cannot report its version is not kept" {
	prepare
	printf '#!/bin/sh\nexit 1\n' >"$RUNOVA_ROOT/usr/local/bin/runova"
	set_src_version 0.2.0
	step_binary
	[ ! -e "$RUNOVA_ROOT/var/lib/runova/rollback" ]
	[ "$("$RUNOVA_ROOT/usr/local/bin/runova" version)" = "runova 0.2.0" ]
}

@test "packages already installed are skipped" {
	step_packages
	: >"$CALLS"
	step_packages
	! grep -q 'apt-get' "$CALLS"
}

@test "only missing packages are installed" {
	mkdir -p "$RUNOVA_ROOT/.installed"
	touch "$RUNOVA_ROOT/.installed/caddy"
	step_packages
	grep -q 'apt-get install -y docker.io postgresql-18 ufw jq curl' "$CALLS"
}

@test "rerun is idempotent" {
	run run_all_steps
	[ "$status" -eq 0 ]
	[[ "$output" == *"Password"* ]]
	run run_all_steps
	[ "$status" -eq 0 ]
	[[ "$output" != *"Password"* ]]
	[ "$(grep -c 'admin create' "$CALLS")" -eq 1 ]
	[ "$(grep -c keygen "$CALLS")" -eq 1 ]
	[ "$(grep -c 'panel set' "$CALLS")" -eq 1 ]
	[ "$(grep -c 'systemctl restart runova' "$CALLS")" -eq 2 ]
}

@test "no public ip summary" {
	TEST_IP=10.0.0.5 run summary
	[[ "$output" == *"Cloudflare Tunnel"* ]]
}

@test "public ip summary has no tunnel advice" {
	TEST_IP=203.0.113.9 run summary
	[[ "$output" != *"Cloudflare Tunnel"* ]]
}

@test "summary prints address, username and password once" {
	ADMIN_PASSWORD=Secret123
	PORT=8443
	TEST_IP=203.0.113.9 run summary
	[[ "$output" == *"https://203.0.113.9:8443"* ]]
	[[ "$output" == *"Username     : admin"* ]]
	[[ "$output" == *"Password     : Secret123"* ]]
}

@test "domain and port exclusive" {
	run parse_args --domain a.co --port 1
	[ "$status" -ne 0 ]
	run parse_args --domain a.co --port 9443
	[ "$status" -ne 0 ]
}

@test "parse_args validates its values" {
	run parse_args --domain 203.0.113.5
	[ "$status" -ne 0 ]
	run parse_args --port 443
	[ "$status" -ne 0 ]
	run parse_args --port 20500
	[ "$status" -ne 0 ]
	run parse_args --public-ip 999.1.1.1
	[ "$status" -ne 0 ]
	run parse_args --bogus
	[ "$status" -ne 0 ]
	run parse_args --domain
	[ "$status" -ne 0 ]
}

@test "parse_args accepts the documented options" {
	parse_args --domain=Panel.A.co --public-ip 2001:db8::1 -y
	[ "$DOMAIN" = panel.a.co ]
	[ "$PUBLIC_IP" = 2001:db8::1 ]
	[ "$ASSUME_YES" = 1 ]
	parse_args --port 8443
	[ "$PORT" = 8443 ] && [ -z "$DOMAIN" ]
}

# progress runs run_steps in a fresh bash with set -e and the EXIT trap, as
# main does, with every step replaced by a stub. Extra arguments are bash code
# that redefines steps. RUNOVA_PROGRESS picks plain or tty output.
progress() {
	RUNOVA_LOG="$BATS_TEST_TMPDIR/install.log" bash -c '
		source "$1" --lib
		shift
		for s in packages user_dirs binary secret env_file docker_daemon caddy postgres panel admin firewall service verify; do
			eval "step_$s() { :; }"
		done
		eval "$1"
		set -euo pipefail
		trap on_exit EXIT
		run_steps
		INSTALL_DONE=1
	' _ "$BATS_TEST_DIRNAME/../install.sh" "$1"
}

@test "plain progress: one line per step, no carriage return, details path printed once" {
	RUNOVA_PROGRESS=plain run progress ':'
	[ "$status" -eq 0 ]
	[[ $output != *$'\r'* ]]
	[[ $output == *"Details: $BATS_TEST_TMPDIR/install.log"* ]]
	[ "$(grep -c 'Details:' <<<"$output")" -eq 1 ]
	[ "$(grep -cE '^\[[0-9]+/13\] .* \.\.\. (done \([0-9]+s\)|already done)$' <<<"$output")" -eq 13 ]
	[[ $output == *"[2/13] Creating user and folders ... done (0s)"* ]]
}

@test "step output goes to the log, not the terminal; the log is 0600" {
	RUNOVA_PROGRESS=plain run progress 'step_secret() { echo NOISY-LINE; }'
	[ "$status" -eq 0 ]
	[[ $output != *NOISY-LINE* ]]
	grep -q NOISY-LINE "$BATS_TEST_TMPDIR/install.log"
	[ "$(stat -c %a "$BATS_TEST_TMPDIR/install.log")" = 600 ]
}

@test "the log is created anew on every run" {
	echo OLD >"$BATS_TEST_TMPDIR/install.log"
	RUNOVA_PROGRESS=plain run progress ':'
	! grep -q OLD "$BATS_TEST_TMPDIR/install.log"
}

@test "a step that was already done says so" {
	RUNOVA_PROGRESS=plain run progress 'step_secret() { STEP_SKIPPED=1; }'
	[[ $output == *"[4/13] Creating the encryption key ... already done"* ]]
}

@test "a failing step stops the install with the log tail and a non-zero exit" {
	RUNOVA_PROGRESS=plain run progress 'step_caddy() { seq 1 50 | sed "s/^/log line /"; return 7; }'
	[ "$status" -ne 0 ]
	[[ $output == *"[7/13] Configuring Caddy ... failed ("* ]]
	[[ $output == *"log line 50"* ]]
	[[ $output == *"log line 21"* ]]
	[[ $output != *"log line 20"$'\n'* ]]
	[[ $output == *"$BATS_TEST_TMPDIR/install.log"* ]]
	[[ $output == *"The installation stopped before finishing"* ]]
	[[ $output != *"[8/13]"* ]]
}

@test "die in a step prints failed, the log tail, then the error message" {
	RUNOVA_PROGRESS=plain run progress 'step_postgres() { echo from-the-log; die "PostgreSQL is broken"; }'
	[ "$status" -eq 1 ]
	[[ $output == *"[8/13] Configuring PostgreSQL ... failed"* ]]
	[[ $output == *"from-the-log"* ]]
	[[ $output == *"Error: PostgreSQL is broken"* ]]
	[[ ${output%%Error:*} == *"failed"* ]]
}

@test "notice and warn from a step stay visible on the terminal" {
	RUNOVA_PROGRESS=plain run progress 'step_caddy() { notice "shown to the user"; warn "careful"; }'
	[ "$status" -eq 0 ]
	[[ $output == *"shown to the user"* ]]
	[[ $output == *"Warning: careful"* ]]
}

@test "tty progress animates one line with carriage returns and ends with done" {
	RUNOVA_PROGRESS=tty run progress 'step_packages() { sleep 0.5; }'
	[ "$status" -eq 0 ]
	[[ $output == *$'\r'* ]]
	[[ $output == *"[1/13] Installing packages (docker.io, caddy, postgresql-18, ufw, jq, curl)"* ]]
	[[ $output == *"[1/13] Installing packages (docker.io, caddy, postgresql-18, ufw, jq, curl) ... done ("* ]]
}

@test "the spinner is gone after success, failure and a signal" {
	local pidfile="$BATS_TEST_TMPDIR/pid"
	RUNOVA_PROGRESS=tty run progress 'step_packages() { echo $SPINNER_PID >"'"$pidfile"'"; sleep 0.3; }'
	[ "$status" -eq 0 ]
	! kill -0 "$(cat "$pidfile")" 2>/dev/null

	RUNOVA_PROGRESS=tty run progress 'step_packages() { echo $SPINNER_PID >"'"$pidfile"'"; sleep 0.3; return 3; }'
	[ "$status" -ne 0 ]
	! kill -0 "$(cat "$pidfile")" 2>/dev/null

	RUNOVA_PROGRESS=tty run progress 'step_packages() { echo $SPINNER_PID >"'"$pidfile"'"; kill -INT $$; sleep 1; }'
	[ "$status" -eq 130 ]
	[[ $output != *"[2/13]"* ]]
	! kill -0 "$(cat "$pidfile")" 2>/dev/null
}

@test "elapsed time is formatted as seconds or minutes" {
	local t
	fmt_elapsed 7 t
	[ "$t" = 7s ]
	fmt_elapsed 72 t
	[ "$t" = 1m12s ]
	fmt_elapsed 600 t
	[ "$t" = 10m00s ]
}

@test "package step label lists what will be installed" {
	mkdir -p "$RUNOVA_ROOT/.installed"
	touch "$RUNOVA_ROOT/.installed/caddy"
	missing_packages
	[ "${MISSING[*]}" = "docker.io postgresql-18 ufw jq curl" ]
}
