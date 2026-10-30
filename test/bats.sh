#!/usr/bin/env bash
# Runs the installer tests and shellcheck in Docker (neither bats nor shellcheck needs to be installed).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

docker build -q -t runova-bats -f test/Dockerfile.bats test >/dev/null
docker run --rm -v "$PWD:/code" -w /code runova-bats test/install.bats
docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable install.sh scripts/release.sh test/bats.sh test/e2e/*.sh
