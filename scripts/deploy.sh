#!/usr/bin/env bash
# shellcheck disable=SC2029 # the remote command is built on this side on purpose (quoted with %q)
# Builds a release and installs (or updates) Runova on an Ubuntu 26.04 server over SSH.
#
#   scripts/deploy.sh [options] user@host [install.sh options...]
#
# Run it from the developer machine. Everything after user@host that this script
# does not know is handed to install.sh on the server (--domain, --port, --yes, ...).

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() {
	printf 'Error: %s\n' "$*" >&2
	exit 1
}

log() { printf '==> %s\n' "$*"; }

usage() {
	cat <<'EOF'
Usage: scripts/deploy.sh [options] user@host [install.sh options...]

Builds a release, copies it to the server over SSH, verifies the checksum, and
runs install.sh on the server with sudo. The same command is used to update.

Deploy options (before or after user@host):
  --version <x.y.z>     release version (default: from "git describe --tags --always --dirty")
  -p, --ssh-port <n>    SSH port of the server (default: 22)
  -i, --identity <file> SSH private key
  -h, --help            show this help

Extra SSH options go in the RUNOVA_SSH_OPTS variable, for example:
  RUNOVA_SSH_OPTS="-o ProxyJump=bastion" scripts/deploy.sh admin@10.0.0.5

Other options are passed on to install.sh (see ./install.sh --help), for example:
  scripts/deploy.sh admin@203.0.113.5 --domain panel.example.com
  scripts/deploy.sh admin@203.0.113.5 --port 8443 --public-ip 203.0.113.5
EOF
}

# default_version derives a version release.sh accepts (X.Y.Z[-suffix]) from git.
default_version() {
	local raw
	raw=$(cd "$root" && git describe --tags --always --dirty 2>/dev/null) || die "Could not determine the version from git; pass --version x.y.z"
	raw=${raw#v}
	if [[ $raw =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		printf '%s\n' "$raw"
	elif [[ $raw =~ ^([0-9]+\.[0-9]+\.[0-9]+)-(.+)$ ]]; then
		printf '%s-%s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]//-/.}"
	else
		printf '0.0.0-dev.%s\n' "${raw//-/.}"
	fi
}

target="" version="" ssh_port="" identity=""
install_args=()

parse_args() {
	while (($# > 0)); do
		case "$1" in
		-h | --help)
			usage
			exit 0
			;;
		--version | -p | --ssh-port | -i | --identity)
			(($# >= 2)) || die "Option $1 needs a value"
			case "$1" in
			--version) version=$2 ;;
			-p | --ssh-port) ssh_port=$2 ;;
			*) identity=$2 ;;
			esac
			shift 2
			;;
		--version=*)
			version=${1#*=}
			shift
			;;
		--ssh-port=*)
			ssh_port=${1#*=}
			shift
			;;
		--identity=*)
			identity=${1#*=}
			shift
			;;
		-*)
			[[ -n $target ]] || die "Unknown option before user@host: $1 (see --help)"
			install_args=("$@")
			return 0
			;;
		*)
			[[ -z $target ]] || {
				install_args=("$@")
				return 0
			}
			target=$1
			shift
			;;
		esac
	done
}

# `source scripts/deploy.sh --lib` defines the functions and runs nothing (for the tests).
if [[ ${1:-} == --lib ]]; then
	# shellcheck disable=SC2317
	return 0 2>/dev/null || exit 0
fi
set -euo pipefail

parse_args "$@"
[[ -n $target ]] || {
	usage >&2
	exit 2
}
[[ $target == *@* && $target != @* && $target != *@ ]] || die "The target must look like user@host, for example admin@203.0.113.5"
[[ -z $ssh_port || $ssh_port =~ ^[0-9]+$ ]] || die "Invalid SSH port: $ssh_port"
[[ -z $identity || -f $identity ]] || die "Key file not found: $identity"
version=${version:-$(default_version)}
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]] || die "Invalid version: $version (example: 0.1.0)"

for cmd in ssh scp sha256sum go; do
	command -v "$cmd" >/dev/null || die "Command '$cmd' not found on this machine"
done

work=$(mktemp -d "${TMPDIR:-/tmp}/runova-deploy.XXXXXX")
remote_dir=""

# SSH multiplexing: a password, if any, is asked once for the whole run.
opts=(-o "ControlMaster=auto" -o "ControlPath=$work/cm" -o "ControlPersist=120")
if [[ -n ${RUNOVA_SSH_OPTS:-} ]]; then
	read -r -a extra_opts <<<"$RUNOVA_SSH_OPTS"
	opts+=("${extra_opts[@]}")
fi
ssh_args=("${opts[@]}")
scp_args=("${opts[@]}")
if [[ -n $ssh_port ]]; then
	ssh_args+=(-p "$ssh_port")
	scp_args+=(-P "$ssh_port")
fi
if [[ -n $identity ]]; then
	ssh_args+=(-i "$identity")
	scp_args+=(-i "$identity")
fi

# remote_sh ARGS...: runs the script on stdin on the server with ARGS as $1...
remote_sh() {
	local q
	printf -v q '%q ' "$@"
	ssh "${ssh_args[@]}" "$target" "bash -s -- $q"
}

cleanup() {
	if [[ -n $remote_dir ]]; then
		ssh "${ssh_args[@]}" "$target" "rm -rf -- $(printf '%q' "$remote_dir")" >/dev/null 2>&1 || true
	fi
	ssh "${ssh_args[@]}" -O exit "$target" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

log "Checking server $target"
info=""
rc=0
info=$(
	remote_sh <<'EOF'
. /etc/os-release 2>/dev/null
echo "ID=${ID:-}"
echo "VERSION_ID=${VERSION_ID:-}"
echo "ARCH=$(uname -m)"
echo "UID=$(id -u)"
if sudo -n true 2>/dev/null; then
	echo "SUDO=nopasswd"
elif id -nG | tr ' ' '\n' | grep -qxE 'sudo|admin|wheel'; then
	echo "SUDO=password"
else
	echo "SUDO=none"
fi
EOF
) || rc=$?
if ((rc != 0)); then
	if ((rc == 255)); then
		die "Could not connect to $target over SSH. Check the address/IP, the port (use -p if it is not 22), the key (use -i), and the provider's firewall. Try first: ssh ${ssh_port:+-p $ssh_port }$target"
	fi
	die "The check on the server failed (code $rc). Make sure $target has a bash shell"
fi

field() { sed -n "s/^$1=//p" <<<"$info" | head -n 1; }
os_id=$(field ID)
os_version=$(field VERSION_ID)
os_arch=$(field ARCH)
remote_uid=$(field UID)
sudo_mode=$(field SUDO)

if [[ $os_id != ubuntu || $os_version != 26.04 ]]; then
	die "The server runs ${os_id:-an unknown OS} ${os_version}, but Runova only supports Ubuntu Server 26.04. Reinstall the server with Ubuntu Server 26.04 LTS."
fi
if [[ $os_arch != x86_64 ]]; then
	die "The server architecture is $os_arch, but Runova only supports amd64 (x86_64). Use an amd64 server."
fi
sudo_cmd="sudo"
if [[ $remote_uid == 0 ]]; then
	sudo_cmd=""
elif [[ $sudo_mode == none ]]; then
	die "User ${target%@*} has no sudo rights on the server. On the server, as root, run: usermod -aG sudo ${target%@*}; then log in again and repeat this command."
fi

log "Building release $version"
(cd "$root" && scripts/release.sh "$version") >/dev/null || die "Building the release failed; run scripts/release.sh $version to see why"
archive="runova-$version-linux-amd64.tar.gz"
[[ -f $root/dist/$archive && -f $root/dist/SHA256SUMS ]] || die "dist/$archive or dist/SHA256SUMS is missing after the build"

remote_dir=$(remote_sh "$version" <<'EOF'
mktemp -d "/tmp/runova-$1.XXXXXX"
EOF
) || die "Could not create a temporary folder on the server"
[[ $remote_dir == /tmp/runova-* ]] || die "Unexpected temporary folder on the server: $remote_dir"

log "Copying $archive and SHA256SUMS to $target:$remote_dir"
scp -q "${scp_args[@]}" "$root/dist/$archive" "$root/dist/SHA256SUMS" "$target:$remote_dir/" ||
	die "Copying to the server failed. Make sure scp/sftp is allowed for ${target%@*} and the server's /tmp has enough space"

log "Verifying the checksum and extracting on the server"
remote_sh "$remote_dir" "$archive" <<'EOF' || die "Checksum mismatch or corrupt archive; repeat this command. If it keeps failing, rebuild with scripts/release.sh."
set -e
cd "$1"
sha256sum -c SHA256SUMS
tar xzf "$2"
EOF

log "Running install.sh on the server (sudo may ask for the password of $(printf '%s' "${target%@*}"))"
tty_flag=-t
[[ -t 0 ]] || tty_flag=-tt
install_cmd="cd $(printf '%q' "$remote_dir/runova-$version-linux-amd64") && $sudo_cmd ./install.sh"
if ((${#install_args[@]} > 0)); then
	printf -v quoted '%q ' "${install_args[@]}"
	install_cmd+=" $quoted"
fi
rc=0
ssh "$tty_flag" "${ssh_args[@]}" "$target" "$install_cmd" || rc=$?
if ((rc != 0)); then
	die "install.sh on the server stopped with code $rc. Read the messages above, fix the problem, and run the same command again; steps that are already done are skipped."
fi
