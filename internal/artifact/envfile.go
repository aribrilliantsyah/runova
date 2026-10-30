package artifact

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const maxEnvLine = 1 << 20

// ParseEnvExample reads a .env.example. Lines are NAME=value (optionally
// prefixed by export); the value becomes the default with surrounding quotes
// removed. A `# secret` comment on the preceding comment line or at the end of
// the line marks the variable secret. Blank lines, other comments and lines
// that are not valid variable assignments are ignored. The result is sorted
// by name with duplicates merged (first default wins).
func ParseEnvExample(r io.Reader) ([]EnvVar, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, maxEnvLine)
	set := envSet{}
	pendingSecret := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			pendingSecret = false
			continue
		case line[0] == '#':
			if isSecretComment(line[1:]) {
				pendingSecret = true
			}
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, rest, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !validEnvName(name) {
			pendingSecret = false
			continue
		}
		value, comment := splitValue(strings.TrimSpace(rest))
		set.add(EnvVar{
			Name:    name,
			Default: value,
			Secret:  pendingSecret || isSecretComment(comment),
			Source:  SourceEnvExample,
		})
		pendingSecret = false
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read .env.example: %w", err)
	}
	return set.list(), nil
}

func isSecretComment(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "secret")
}

// splitValue separates an assignment value from a trailing `# comment`.
func splitValue(s string) (value, comment string) {
	if s == "" {
		return "", ""
	}
	if q := s[0]; q == '"' || q == '\'' {
		if end := strings.IndexByte(s[1:], q); end >= 0 {
			value = s[1 : 1+end]
			if _, c, ok := strings.Cut(s[2+end:], "#"); ok {
				comment = c
			}
			return value, comment
		}
	}
	if i := strings.Index(s, " #"); i >= 0 {
		return strings.TrimSpace(s[:i]), s[i+2:]
	}
	if i := strings.Index(s, "\t#"); i >= 0 {
		return strings.TrimSpace(s[:i]), s[i+2:]
	}
	return s, ""
}
