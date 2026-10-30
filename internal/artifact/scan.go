package artifact

import (
	"bytes"
	"io"
	"slices"
	"strings"
)

const (
	scanChunk    = 1 << 20
	scanMaxEnv   = 50
	scanMaxCands = 4096
	scanMinName  = 4
	scanMaxName  = 64
	scanOverflow = scanMaxName + 1
)

var scanMarkers = [][]byte{
	[]byte("os.Getenv"),
	[]byte("syscall.Getenv"),
	[]byte("std::env::var"),
	[]byte("getenv"),
}

var scanIgnored = map[string]bool{
	"GODEBUG": true, "GOMAXPROCS": true, "GOTRACEBACK": true, "GOGC": true, "GOMEMLIMIT": true,
	"GOROOT": true, "GOPATH": true, "RUST_BACKTRACE": true, "RUST_LIB_BACKTRACE": true,
	"LD_LIBRARY_PATH": true, "LD_PRELOAD": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "TZ": true, "HOME": true,
	"PATH": true, "USER": true, "LANG": true,
}

var scanIgnoredPrefixes = []string{"GO", "CGO_", "RUST_", "LC_", "XDG_", "GLIBC_"}

// scanLikely marks words typical of configuration variables. When a binary
// has more candidates than scanMaxEnv (any net/http program carries hundreds
// of protocol constants), only names containing one of these are kept.
var scanLikely = []string{"PASSWORD", "SECRET", "TOKEN", "KEY", "URL", "URI", "DSN", "HOST", "PORT", "ADDR", "DATABASE", "DB_", "USER", "ENV", "MODE", "LEVEL"}

func scanLikelyName(name string) bool {
	return slices.ContainsFunc(scanLikely, func(w string) bool { return strings.Contains(name, w) })
}

func isEnvChar(c byte) bool { return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' }

// scanCandidate reports whether a run of [A-Z0-9_] bytes looks like an env var name.
func scanCandidate(name string) bool {
	if len(name) < scanMinName || len(name) > scanMaxName || scanIgnored[name] {
		return false
	}
	for _, p := range scanIgnoredPrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	if name == "PORT" {
		return true
	}
	return name[0] >= 'A' && name[0] <= 'Z' && strings.Contains(name[1:], "_")
}

// scanEnv streams r looking for env var names. Names count only when the data
// also holds a marker for reading the environment. Names are runs of
// [A-Z0-9_]; leading digits and underscores are dropped. Result is sorted, at
// most scanMaxEnv entries (only likely config names when there are more candidates), and never includes file contents beyond those names.
func scanEnv(r io.Reader) ([]EnvVar, error) {
	var (
		buf      = make([]byte, scanChunk)
		carry    []byte
		run      = make([]byte, 0, scanOverflow)
		overflow bool
		found    bool
		names    []string
		seen     = map[string]struct{}{}
	)
	endRun := func() {
		if !overflow {
			name := strings.TrimLeft(string(run), "0123456789_")
			if _, dup := seen[name]; !dup && len(names) < scanMaxCands && scanCandidate(name) {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
		run, overflow = run[:0], false
	}
	maxMarker := 0
	for _, m := range scanMarkers {
		maxMarker = max(maxMarker, len(m))
	}
	for {
		n, err := io.ReadFull(r, buf)
		chunk := buf[:n]
		if !found {
			window := append(carry, chunk...)
			for _, m := range scanMarkers {
				if bytes.Contains(window, m) {
					found = true
					break
				}
			}
			carry = append(carry[:0], window[max(0, len(window)-(maxMarker-1)):]...)
		}
		for _, c := range chunk {
			switch {
			case isEnvChar(c):
				if len(run) < scanOverflow {
					run = append(run, c)
				} else {
					overflow = true
				}
			case len(run) > 0:
				endRun()
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	endRun()
	if !found {
		return []EnvVar{}, nil
	}
	if len(names) > scanMaxEnv {
		names = slices.DeleteFunc(names, func(n string) bool { return !scanLikelyName(n) })
		names = names[:min(len(names), scanMaxEnv)]
	}
	slices.Sort(names)
	out := make([]EnvVar, len(names))
	for i, name := range names {
		out[i] = EnvVar{Name: name, Secret: secretName(name), Source: SourceScan}
	}
	return out, nil
}
