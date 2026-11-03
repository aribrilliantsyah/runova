#!/usr/bin/env bash
# Downloads a Runova release from GitHub, verifies it and runs its installer.
#
#   curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- [opsi]
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
			(($# >= 2)) || die "Opsi --version butuh nilai (contoh: --version 0.1.0)"
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

	((EUID == 0)) || die "Jalankan sebagai root: curl ... | sudo bash -s -- [opsi]"
	command -v curl >/dev/null || die "curl tidak ditemukan; pasang dengan: apt-get install -y curl"
	command -v sha256sum >/dev/null || die "sha256sum tidak ditemukan"

	if [[ -z $version ]]; then
		version=$(latest_version) || die "Tidak bisa membaca rilis terbaru dari github.com/$REPO"
	fi
	[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]] || die "Versi tidak valid: $version"

	local name="runova-${version}-linux-amd64"
	local base="$GITHUB/$REPO/releases/download/v${version}"
	local work
	work=$(mktemp -d /tmp/runova-get.XXXXXX)
	# shellcheck disable=SC2064 # expand now: $work is local to main
	trap "rm -rf '$work'" EXIT

	printf 'Mengunduh Runova %s dari github.com/%s ...\n' "$version" "$REPO"
	curl -fsSL --retry 3 -o "$work/$name.tar.gz" "$base/$name.tar.gz" || die "Gagal mengunduh $base/$name.tar.gz"
	curl -fsSL --retry 3 -o "$work/SHA256SUMS" "$base/SHA256SUMS" || die "Gagal mengunduh $base/SHA256SUMS"
	(cd "$work" && grep -E "  $name\.tar\.gz\$" SHA256SUMS | sha256sum -c - >/dev/null) || die "Checksum arsip tidak cocok; unduhan rusak atau diubah"

	tar -xzf "$work/$name.tar.gz" -C "$work"
	(cd "$work/$name" && sha256sum -c SHA256SUMS >/dev/null) || die "Checksum isi arsip tidak cocok"

	# install.sh asks for confirmation on a terminal; under `curl | bash` stdin is the
	# script, so give it the real terminal when there is one.
	if [[ -t 1 && -r /dev/tty ]] && { : </dev/tty; } 2>/dev/null; then
		"$work/$name/install.sh" "${args[@]}" </dev/tty
	else
		"$work/$name/install.sh" "${args[@]}"
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
