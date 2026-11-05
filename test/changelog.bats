#!/usr/bin/env bats
# Tests for scripts/changelog.sh, which turns one CHANGELOG.md section into release notes.

setup() {
	LOG="$BATS_TEST_TMPDIR/CHANGELOG.md"
	cat >"$LOG" <<'EOF'
# Changelog

Intro text.

## [0.2.0] - 2026-01-02

### Added
- Second thing.

## [0.1.10] - 2026-01-01

### Fixed
- Tenth patch.

## [0.1.1] - 2026-01-01

### Fixed
- First patch.

[0.2.0]: https://example.com/v0.2.0
[0.1.1]: https://example.com/v0.1.1
EOF
}

@test "prints only the section of the version, without its heading" {
	run scripts/changelog.sh 0.2.0 "$LOG"
	[ "$status" -eq 0 ]
	[ "${lines[0]}" = "### Added" ]
	[ "${lines[1]}" = "- Second thing." ]
	[ "${#lines[@]}" -eq 2 ]
}

@test "0.1.1 is not confused with 0.1.10" {
	run scripts/changelog.sh 0.1.1 "$LOG"
	[ "$status" -eq 0 ]
	[ "${lines[1]}" = "- First patch." ]
	[[ $output != *Tenth* ]]
}

@test "the last section stops before the link references" {
	run scripts/changelog.sh 0.1.1 "$LOG"
	[[ $output != *example.com* ]]
}

@test "a version without a section fails and names it" {
	run scripts/changelog.sh 9.9.9 "$LOG"
	[ "$status" -ne 0 ]
	[[ $output == *"no section for 9.9.9"* ]]
}

@test "every version linked at the bottom of both changelogs has a section" {
	for f in CHANGELOG.md CHANGELOG.id.md; do
		versions=$(sed -n 's/^\[\([0-9][0-9.]*\)\]: .*/\1/p' "$f")
		[ -n "$versions" ]
		for v in $versions; do
			run scripts/changelog.sh "$v" "$f"
			[ "$status" -eq 0 ] || { echo "$f: no section for $v"; return 1; }
		done
	done
}
