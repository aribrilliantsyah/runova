package artifact

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// classBytes returns the 8-byte header of a class file with the given major version.
func classBytes(major uint16) []byte {
	b := []byte{0xCA, 0xFE, 0xBA, 0xBE, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[6:], major)
	return b
}

func manifest(attrs ...string) []byte {
	var sb strings.Builder
	sb.WriteString("Manifest-Version: 1.0\r\n")
	for i := 0; i+1 < len(attrs); i += 2 {
		sb.WriteString(attrs[i] + ": " + attrs[i+1] + "\r\n")
	}
	sb.WriteString("\r\n")
	return []byte(sb.String())
}

func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildJar writes a JAR with the given entries to a temp dir and returns its path.
func buildJar(t *testing.T, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.jar")
	if err := os.WriteFile(path, zipBytes(t, files), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func envNames(env []EnvVar) []string {
	out := make([]string, len(env))
	for i, e := range env {
		out[i] = e.Name
	}
	return out
}

func hasProblem(r Report, substr string) bool {
	return slices.ContainsFunc(r.Problems, func(p string) bool { return strings.Contains(p, substr) })
}
