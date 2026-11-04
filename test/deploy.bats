#!/usr/bin/env bats
# Tests for scripts/deploy.sh. ssh, scp, go and git are stubs from
# test/fixtures/deploy-bin that append their calls to $CALLS; the checks that
# run before the release is built are exercised through the real script.

setup() {
	export CALLS="$BATS_TEST_TMPDIR/calls"
	export PATH="$BATS_TEST_DIRNAME/fixtures/deploy-bin:$PATH"
	DEPLOY="$BATS_TEST_DIRNAME/../scripts/deploy.sh"
	: >"$CALLS"
	# shellcheck source=../scripts/deploy.sh
	source "$DEPLOY" --lib
}

probe() {
	printf 'ID=%s\nVERSION_ID=%s\nARCH=%s\nUID=%s\nSUDO=%s\n' "${1:-ubuntu}" "${2:-26.04}" "${3:-x86_64}" "${4:-1000}" "${5:-nopasswd}"
}

@test "version: a tag becomes the version" {
	GIT_DESCRIBE=v0.1.0 run default_version
	[ "$output" = 0.1.0 ]
}

@test "version: commits after a tag and a dirty tree stay a valid release version" {
	GIT_DESCRIBE=v0.1.0-3-gabc1234-dirty run default_version
	[ "$output" = 0.1.0-3.gabc1234.dirty ]
	[[ $output =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]
}

@test "version: without tags the commit hash is used" {
	GIT_DESCRIBE=abc1234-dirty run default_version
	[ "$output" = 0.0.0-dev.abc1234.dirty ]
}

@test "version: no git history is an error that names --version" {
	run default_version
	[ "$status" -ne 0 ]
	[[ "$output" == *"--version"* ]]
}

@test "args: options before and after the target, the rest goes to install.sh" {
	parse_args -p 2222 -i /dev/null --version 1.2.3 admin@host --domain panel.contoh.id --yes
	[ "$ssh_port" = 2222 ]
	[ "$identity" = /dev/null ]
	[ "$version" = 1.2.3 ]
	[ "$target" = admin@host ]
	[ "${install_args[*]}" = "--domain panel.contoh.id --yes" ]
}

@test "args: deploy options can follow the target" {
	parse_args admin@host --ssh-port=2200 --version=2.0.0
	[ "$ssh_port" = 2200 ]
	[ "$version" = 2.0.0 ]
	[ "${#install_args[@]}" -eq 0 ]
}

@test "args: unknown option before the target is rejected" {
	run parse_args --bogus admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"--bogus"* ]]
}

@test "args: an option without a value is rejected" {
	run parse_args admin@host -p
	[ "$status" -ne 0 ]
}

@test "target must be user@host" {
	run "$DEPLOY" 203.0.113.5
	[ "$status" -ne 0 ]
	[[ "$output" == *"user@host"* ]]
	[ ! -s "$CALLS" ]
}

@test "ssh port must be a number" {
	run "$DEPLOY" -p abc admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"Invalid SSH port"* ]]
}

@test "missing identity file is reported before connecting" {
	run "$DEPLOY" -i "$BATS_TEST_TMPDIR/missing" admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"Key file not found"* ]]
	[ ! -s "$CALLS" ]
}

@test "invalid version is rejected" {
	run "$DEPLOY" --version abc admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"Invalid version"* ]]
}

@test "ssh failure (255) says how to check the connection and builds nothing" {
	SSH_RC=255 run "$DEPLOY" --version 1.0.0 -p 2200 admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"Could not connect"* ]]
	[[ "$output" == *"ssh -p 2200 admin@host"* ]]
	! grep -q '^go ' "$CALLS"
	! grep -q '^scp ' "$CALLS"
}

@test "a server that is not Ubuntu 26.04 is refused with what to do" {
	SSH_PROBE=$(probe ubuntu 24.04) run "$DEPLOY" --version 1.0.0 admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"Ubuntu Server 26.04"* ]]
	[[ "$output" == *"24.04"* ]]
	! grep -q '^scp ' "$CALLS"
}

@test "another distribution is refused" {
	SSH_PROBE=$(probe debian 13) run "$DEPLOY" --version 1.0.0 admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"debian"* ]]
}

@test "a non-amd64 server is refused" {
	SSH_PROBE=$(probe ubuntu 26.04 aarch64) run "$DEPLOY" --version 1.0.0 admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"aarch64"* ]]
	[[ "$output" == *"amd64"* ]]
}

@test "a user without sudo is refused with the command to fix it" {
	SSH_PROBE=$(probe ubuntu 26.04 x86_64 1000 none) run "$DEPLOY" --version 1.0.0 admin@host
	[ "$status" -ne 0 ]
	[[ "$output" == *"sudo"* ]]
	[[ "$output" == *"usermod -aG sudo admin"* ]]
	! grep -q '^scp ' "$CALLS"
}

@test "the probe reuses the connection options: port, identity and RUNOVA_SSH_OPTS" {
	touch "$BATS_TEST_TMPDIR/id"
	SSH_PROBE=$(probe ubuntu 24.04) RUNOVA_SSH_OPTS="-o ProxyJump=bastion" \
		run "$DEPLOY" --version 1.0.0 -p 2200 -i "$BATS_TEST_TMPDIR/id" admin@host
	grep -qE '^ssh .*-o ProxyJump=bastion .*-p 2200 -i '"$BATS_TEST_TMPDIR"'/id admin@host' "$CALLS"
}
