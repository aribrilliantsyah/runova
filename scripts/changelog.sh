#!/usr/bin/env bash
# Prints the CHANGELOG.md section of one version, without its "## [X.Y.Z]" heading,
# for the GitHub release notes. Fails when the version has no section, so every
# release tag needs its changelog entry first.
#
#   scripts/changelog.sh 0.3.9 [CHANGELOG.md]
set -euo pipefail

version=${1:?usage: scripts/changelog.sh X.Y.Z [CHANGELOG.md]}
file=${2:-CHANGELOG.md}
[[ -r $file ]] || {
	echo "changelog.sh: cannot read $file" >&2
	exit 1
}

notes=$(awk -v v="$version" '
	/^## \[/ { if (on) exit; if (index($0, "## [" v "]") == 1) { on = 1; next } }
	/^\[[^]]+\]: / { if (on) exit }
	on { print }
' "$file")

# Trim blank lines at both ends.
notes=$(printf '%s\n' "$notes" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')
if [[ -z $notes ]]; then
	echo "changelog.sh: no section for $version in $file" >&2
	exit 1
fi
printf '%s\n' "$notes"
