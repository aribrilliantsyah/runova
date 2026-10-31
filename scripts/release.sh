#!/usr/bin/env bash
# Builds dist/runova-<versi>-linux-amd64.tar.gz and dist/SHA256SUMS.
# Usage: scripts/release.sh <versi>   (for example 0.1.0)
set -euo pipefail

version="${1:-}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]; then
	echo "Penggunaan: scripts/release.sh <versi>   (contoh: 0.1.0)" >&2
	exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

name="runova-${version}-linux-amd64"
dist="$root/dist"
stage="$dist/stage/$name"
archive="$name.tar.gz"

rm -rf "${dist:?}/stage" "${dist:?}/$archive" "${dist:?}/SHA256SUMS"
mkdir -p "$stage"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
	-ldflags "-s -w -X main.version=${version}" -o "$stage/runova" ./cmd/runova
install -m 0755 install.sh "$stage/install.sh"
install -m 0644 packaging/runova.service "$stage/runova.service"
install -m 0644 LICENSE "$stage/LICENSE"
install -m 0644 NOTICE "$stage/NOTICE"
(cd "$stage" && sha256sum runova install.sh runova.service LICENSE NOTICE >SHA256SUMS)

# Only the six files are listed, so the archive has no directory entries.
(cd "$dist/stage" && tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
	-czf "$dist/$archive" \
	"$name/runova" "$name/install.sh" "$name/runova.service" "$name/LICENSE" "$name/NOTICE" "$name/SHA256SUMS")
rm -rf "$dist/stage"

(cd "$dist" && sha256sum "$archive" >SHA256SUMS)
echo "dist/$archive"
