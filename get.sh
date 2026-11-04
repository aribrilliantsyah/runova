#!/usr/bin/env bash
# Downloads a Runova release from GitHub, verifies it and runs its installer.
#
#   curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- [options]
#
# Options of its own: --version X.Y.Z (default: the latest release). Every other
# option is passed to install.sh (--domain, --port, --public-ip, --yes, --skip-firewall).
# RUNOVA_REPO overrides the GitHub repository (owner/name); RUNOVA_GITHUB
# overrides https://github.com (tests point it at a local server).
set -euo pipefail

REPO="${RUNOVA_REPO:-aribrilliantsyah/runova}"
GITHUB="${RUNOVA_GITHUB:-https://github.com}"

die() {
	printf 'runova: %s\n' "$*" >&2
	exit 1
}

main() {
	local version="" args=()
	while (($# > 0)); do
		case "$1" in
		--version)
			(($# >= 2)) || die "Option --version needs a value (example: --version 0.1.0)"
			version="${2#v}"
			shift 2
			;;
		--version=*)
			version="${1#*=}"
			version="${version#v}"
			shift
			;;
		*)
			args+=("$1")
			shift
			;;
		esac
	done

	((EUID == 0)) || die "Run as root: curl ... | sudo bash -s -- [options]"
	command -v curl >/dev/null || die "curl not found; install it with: apt-get install -y curl"
	command -v sha256sum >/dev/null || die "sha256sum not found"

	if [[ -z $version ]]; then
		version=$(latest_version) || die "Could not read the latest release from github.com/$REPO"
	fi
	[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]] || die "Invalid version: $version"

	local name="runova-${version}-linux-amd64"
	local base="$GITHUB/$REPO/releases/download/v${version}"
	local work
	work=$(mktemp -d /tmp/runova-get.XXXXXX)
	# shellcheck disable=SC2064 # expand now: $work is local to main
	trap "rm -rf '$work'" EXIT

	printf 'Downloading Runova %s from github.com/%s ...\n' "$version" "$REPO"
	download "$base/$name.tar.gz" "$work/$name.tar.gz" || die "Could not download $base/$name.tar.gz"
	download "$base/SHA256SUMS" "$work/SHA256SUMS" || die "Could not download $base/SHA256SUMS"
	(cd "$work" && grep -E "  $name\.tar\.gz\$" SHA256SUMS | sha256sum -c - >/dev/null) || die "Archive checksum does not match; the download is corrupt or was changed"

	tar -xzf "$work/$name.tar.gz" -C "$work"
	(cd "$work/$name" && sha256sum -c SHA256SUMS >/dev/null) || die "Checksum of the archive contents does not match"

	# Under `curl | sudo bash`, stdin is the script. Ask here, reading the answer
	# from /dev/tty, then run the installer with --yes and stdin from /dev/null.
	# Handing /dev/tty to a child instead stops it on sudo-rs, Ubuntu 26.04's
	# default sudo: sudo-rs runs the command on its own pty, whose foreground
	# process group is the bash reading the script, so the child's terminal read
	# gets SIGTTIN (observed; classic sudo 1.9.17 does not do this).
	local installer="$work/$name/install.sh"
	if ! has_yes "${args[@]}" && ! has_help "${args[@]}"; then
		{ : </dev/tty; } 2>/dev/null || die "No terminal to ask on; add --yes for an unattended install"
		"$installer" --plan
		local answer
		read -r -p "Continue? [y/N] " answer </dev/tty || answer=""
		[[ $answer == [yY]* ]] || die "Canceled"
		args+=(--yes)
	fi
	"$installer" "${args[@]}" </dev/null
}

has_yes() {
	local a
	for a in "$@"; do [[ $a == --yes || $a == -y ]] && return 0; done
	return 1
}

has_help() {
	local a
	for a in "$@"; do [[ $a == --help || $a == -h ]] && return 0; done
	return 1
}

# download URL FILE: fetches URL into FILE, with a progress bar when stdout is
# a terminal and silently otherwise.
download() {
	if [[ -t 1 ]]; then
		curl -fL --retry 3 --progress-bar -o "$2" "$1"
	else
		curl -fsSL --retry 3 -o "$2" "$1"
	fi
}

# latest_version prints the version of the latest release, without the "v".
latest_version() {
	local url
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$GITHUB/$REPO/releases/latest") || return 1
	[[ $url == */releases/tag/v* ]] || return 1
	printf '%s\n' "${url##*/tag/v}"
}

if [[ ${1:-} != --lib ]]; then
	main "$@"
fi
