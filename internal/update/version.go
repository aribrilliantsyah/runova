// Package update finds out whether a newer Runova release exists: version
// comparison, the GitHub release lookup and a scheduled checker.
package update

import (
	"regexp"
	"strconv"
	"strings"
)

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

type version struct {
	core [3]int
	pre  []string
}

func parse(s string) (version, bool) {
	m := semver.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	var v version
	for i := range v.core {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return version{}, false
		}
		v.core[i] = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	return v, true
}

// Valid reports whether s is a version Compare understands.
func Valid(s string) bool {
	_, ok := parse(s)
	return ok
}

// Compare orders two X.Y.Z[-pre] versions (a leading "v" is ignored): -1, 0 or
// 1. A pre-release is lower than the same release. ok is false when either
// side cannot be parsed.
func Compare(a, b string) (cmp int, ok bool) {
	va, okA := parse(a)
	vb, okB := parse(b)
	if !okA || !okB {
		return 0, false
	}
	for i := range va.core {
		if va.core[i] != vb.core[i] {
			return sign(va.core[i] - vb.core[i]), true
		}
	}
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0, true
	case len(va.pre) == 0:
		return 1, true
	case len(vb.pre) == 0:
		return -1, true
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		if c := comparePre(va.pre[i], vb.pre[i]); c != 0 {
			return c, true
		}
	}
	return sign(len(va.pre) - len(vb.pre)), true
}

// comparePre follows semver: numeric identifiers compare as numbers and sort
// below alphanumeric ones, which compare as text.
func comparePre(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return sign(na - nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
