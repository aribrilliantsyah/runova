#!/usr/bin/env bash
# shellcheck disable=SC2029 # the remote command is built on this side on purpose (quoted with %q)
# Builds a release and installs (or updates) Runova on an Ubuntu 26.04 server over SSH.
#
#   scripts/deploy.sh [opsi] user@host [opsi install.sh...]
#
# Run it from the developer machine. Everything after user@host that this script
# does not know is handed to install.sh on the server (--domain, --port, --yes, ...).

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() {
	printf 'Galat: %s\n' "$*" >&2
	exit 1
}

log() { printf '==> %s\n' "$*"; }

usage() {
	cat <<'EOF'
Penggunaan: scripts/deploy.sh [opsi] user@host [opsi install.sh...]

Membangun rilis, menyalinnya ke server lewat SSH, memeriksa checksum, lalu
menjalankan install.sh di server dengan sudo. Perintah yang sama dipakai untuk
pembaruan.

Opsi deploy (sebelum atau sesudah user@host):
  --version <x.y.z>     versi rilis (bawaan: dari "git describe --tags --always --dirty")
  -p, --ssh-port <n>    port SSH server (bawaan: 22)
  -i, --identity <file> kunci privat SSH
  -h, --help            tampilkan bantuan ini

Opsi SSH tambahan lewat variabel RUNOVA_SSH_OPTS, misalnya:
  RUNOVA_SSH_OPTS="-o ProxyJump=bastion" scripts/deploy.sh admin@10.0.0.5

Opsi lain diteruskan ke install.sh (lihat ./install.sh --help), misalnya:
  scripts/deploy.sh admin@203.0.113.5 --domain panel.contoh.id
  scripts/deploy.sh admin@203.0.113.5 --port 8443 --public-ip 203.0.113.5
EOF
}

# default_version derives a version release.sh accepts (X.Y.Z[-suffix]) from git.
default_version() {
	local raw
	raw=$(cd "$root" && git describe --tags --always --dirty 2>/dev/null) || die "Versi tidak bisa ditentukan dari git; beri --version x.y.z"
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
			(($# >= 2)) || die "Opsi $1 butuh nilai"
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
			[[ -n $target ]] || die "Opsi tidak dikenal sebelum user@host: $1 (lihat --help)"
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
[[ $target == *@* && $target != @* && $target != *@ ]] || die "Tujuan harus berbentuk user@host, misalnya admin@203.0.113.5"
[[ -z $ssh_port || $ssh_port =~ ^[0-9]+$ ]] || die "Port SSH tidak valid: $ssh_port"
[[ -z $identity || -f $identity ]] || die "Berkas kunci tidak ditemukan: $identity"
version=${version:-$(default_version)}
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]] || die "Versi tidak valid: $version (contoh: 0.1.0)"

for cmd in ssh scp sha256sum go; do
	command -v "$cmd" >/dev/null || die "Perintah '$cmd' tidak ditemukan di mesin ini"
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

log "Memeriksa server $target"
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
		die "Tidak bisa tersambung ke $target lewat SSH. Periksa alamat/IP, port (pakai -p bila bukan 22), kunci (pakai -i), dan firewall penyedia server. Coba dulu: ssh ${ssh_port:+-p $ssh_port }$target"
	fi
	die "Pemeriksaan di server gagal (kode $rc). Pastikan $target punya shell bash"
fi

field() { sed -n "s/^$1=//p" <<<"$info" | head -n 1; }
os_id=$(field ID)
os_version=$(field VERSION_ID)
os_arch=$(field ARCH)
remote_uid=$(field UID)
sudo_mode=$(field SUDO)

if [[ $os_id != ubuntu || $os_version != 26.04 ]]; then
	die "Server memakai ${os_id:-OS tidak dikenal} ${os_version}, sedangkan Runova hanya mendukung Ubuntu Server 26.04. Pasang ulang server dengan Ubuntu Server 26.04 LTS."
fi
if [[ $os_arch != x86_64 ]]; then
	die "Server berarsitektur $os_arch, sedangkan Runova hanya mendukung amd64 (x86_64). Pakai server amd64."
fi
sudo_cmd="sudo"
if [[ $remote_uid == 0 ]]; then
	sudo_cmd=""
elif [[ $sudo_mode == none ]]; then
	die "User ${target%@*} tidak punya hak sudo di server. Di server, jalankan sebagai root: usermod -aG sudo ${target%@*}; lalu masuk ulang dan ulangi perintah ini."
fi

log "Membangun rilis $version"
(cd "$root" && scripts/release.sh "$version") >/dev/null || die "Pembangunan rilis gagal; jalankan scripts/release.sh $version untuk melihat sebabnya"
archive="runova-$version-linux-amd64.tar.gz"
[[ -f $root/dist/$archive && -f $root/dist/SHA256SUMS ]] || die "dist/$archive atau dist/SHA256SUMS tidak ada setelah pembangunan"

remote_dir=$(remote_sh "$version" <<'EOF'
mktemp -d "/tmp/runova-$1.XXXXXX"
EOF
) || die "Gagal membuat folder sementara di server"
[[ $remote_dir == /tmp/runova-* ]] || die "Folder sementara di server tidak terduga: $remote_dir"

log "Menyalin $archive dan SHA256SUMS ke $target:$remote_dir"
scp -q "${scp_args[@]}" "$root/dist/$archive" "$root/dist/SHA256SUMS" "$target:$remote_dir/" ||
	die "Penyalinan ke server gagal. Pastikan scp/sftp diizinkan untuk ${target%@*} dan disk /tmp server cukup"

log "Memeriksa checksum dan mengekstrak di server"
remote_sh "$remote_dir" "$archive" <<'EOF' || die "Checksum tidak cocok atau arsip rusak; ulangi perintah ini. Bila tetap gagal, bangun ulang dengan scripts/release.sh."
set -e
cd "$1"
sha256sum -c SHA256SUMS
tar xzf "$2"
EOF

log "Menjalankan install.sh di server (sudo bisa meminta password $(printf '%s' "${target%@*}"))"
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
	die "install.sh di server berhenti dengan kode $rc. Baca pesan di atas, perbaiki, lalu jalankan ulang perintah yang sama; langkah yang sudah selesai dilewati."
fi
