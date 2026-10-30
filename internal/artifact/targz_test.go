package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	typeflag byte
	body     []byte
	link     string
}

func reg(name string, body []byte) tarEntry {
	return tarEntry{name: name, typeflag: tar.TypeReg, body: body}
}

func buildTarGz(t *testing.T, entries ...tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o644, Size: int64(len(e.body)), Linkname: e.link}
		if e.typeflag == tar.TypeDir {
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return writeFile(t, "a.tar.gz", buf.Bytes())
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseEnvExample(t *testing.T) {
	in := "# secret\nDB_PASSWORD=\nexport PORT=9000\nAPI_URL=\"https://x\" \nJWT=abc # secret\n# komentar\nPLAIN='single quoted'\nnot-a-name=1\nlower=2\n\n# secret\n\nAFTER_BLANK=1\n"
	got, err := ParseEnvExample(bytes.NewBufferString(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []EnvVar{
		{Name: "AFTER_BLANK", Default: "1", Source: SourceEnvExample},
		{Name: "API_URL", Default: "https://x", Source: SourceEnvExample},
		{Name: "DB_PASSWORD", Secret: true, Source: SourceEnvExample},
		{Name: "JWT", Default: "abc", Secret: true, Source: SourceEnvExample},
		{Name: "PLAIN", Default: "single quoted", Source: SourceEnvExample},
		{Name: "PORT", Default: "9000", Source: SourceEnvExample},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseEnvExampleSecretByName(t *testing.T) {
	got, err := ParseEnvExample(bytes.NewBufferString("API_TOKEN=x\nSESSION_KEY=y\nHOST=z\n"))
	if err != nil {
		t.Fatal(err)
	}
	secret := map[string]bool{}
	for _, v := range got {
		secret[v.Name] = v.Secret
	}
	if !secret["API_TOKEN"] || !secret["SESSION_KEY"] || secret["HOST"] {
		t.Errorf("secret = %v", secret)
	}
}

func TestTarRejectsTraversalAndLinks(t *testing.T) {
	cases := map[string][]tarEntry{
		"dotdot":   {reg("app", []byte("x")), reg("../x", []byte("x"))},
		"nested":   {reg("a/../../x", []byte("x"))},
		"absolute": {reg("/etc/x", []byte("x"))},
		"symlink":  {reg("app", []byte("x")), {name: "link", typeflag: tar.TypeSymlink, link: "/etc/passwd"}},
		"hardlink": {reg("app", []byte("x")), {name: "link", typeflag: tar.TypeLink, link: "app"}},
		"device":   {{name: "dev", typeflag: tar.TypeChar}},
		"abs dir":  {{name: "/tmp/", typeflag: tar.TypeDir}},
		"too long": {reg(strings.Repeat("a", 300), []byte("x"))},
		"control":  {reg("app\x01evil", []byte("x"))},
	}
	for name, entries := range cases {
		parent := t.TempDir()
		dir := filepath.Join(parent, "work")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		_, _, err := ExtractTarGz(buildTarGz(t, entries...), dir, 1<<20)
		if !errors.Is(err, ErrUnsafeArchive) {
			t.Errorf("%s: err = %v, want ErrUnsafeArchive", name, err)
		}
		if got := dirEntries(t, parent); !slices.Equal(got, []string{"work"}) {
			t.Errorf("%s: files left behind: %v", name, got)
		}
	}
}

func TestTarSizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := buildTarGz(t, reg("app", bytes.Repeat([]byte{0}, 2<<20)))
	if _, _, err := ExtractTarGz(path, dir, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
	if got := dirEntries(t, dir); len(got) != 0 {
		t.Errorf("workDir not emptied: %v", got)
	}
}

func TestTarSizeLimitAcrossEntries(t *testing.T) {
	dir := t.TempDir()
	path := buildTarGz(t,
		reg("app", bytes.Repeat([]byte{1}, 700<<10)),
		reg("README", bytes.Repeat([]byte{1}, 700<<10)))
	if _, _, err := ExtractTarGz(path, dir, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
	if got := dirEntries(t, dir); len(got) != 0 {
		t.Errorf("workDir not emptied: %v", got)
	}
}

func TestTarLyingHeaderSize(t *testing.T) {
	// A header claiming a small size cannot smuggle more bytes: tar reads only Size bytes.
	dir := t.TempDir()
	path := buildTarGz(t, reg("app", []byte("12345")))
	artifact, _, err := ExtractTarGz(path, dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(artifact); st.Size() != 5 {
		t.Errorf("size = %d", st.Size())
	}
}

func TestTarContents(t *testing.T) {
	jar := []byte("PK\x03\x04")
	bad := map[string][]tarEntry{
		"two jars":      {reg("a.jar", jar), reg("b.jar", jar)},
		"jar in subdir": {reg("sub/a.jar", jar)},
		"empty":         {},
		"only env":      {reg(".env.example", []byte("A_B=1\n"))},
		"two env":       {reg("a.jar", jar), reg(".env.example", nil), {name: "./.env.example", typeflag: tar.TypeReg}},
	}
	for name, entries := range bad {
		dir := t.TempDir()
		if _, _, err := ExtractTarGz(buildTarGz(t, entries...), dir, 1<<20); !errors.Is(err, ErrArchiveContents) {
			t.Errorf("%s: err = %v, want ErrArchiveContents", name, err)
		}
		if got := dirEntries(t, dir); len(got) != 0 {
			t.Errorf("%s: files left behind: %v", name, got)
		}
	}

	dir := t.TempDir()
	art, env, err := ExtractTarGz(buildTarGz(t,
		tarEntry{name: "./", typeflag: tar.TypeDir},
		reg("./app.jar", jar), reg(".env.example", []byte("A_B=1\n")), reg("README.md", []byte("hi")), reg("LICENSE", []byte("mit"))),
		dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if art != filepath.Join(dir, "app.jar") || env != filepath.Join(dir, ".env.example") {
		t.Errorf("art=%q env=%q", art, env)
	}
	if got := dirEntries(t, dir); !slices.Equal(got, []string{".env.example", "app.jar"}) {
		t.Errorf("dir = %v", got)
	}
}

func TestTarWithoutEnvExample(t *testing.T) {
	art, env, err := ExtractTarGz(buildTarGz(t, reg("app", []byte("x"))), t.TempDir(), 1<<20)
	if err != nil || art == "" || env != "" {
		t.Errorf("art=%q env=%q err=%v", art, env, err)
	}
}

func TestTarCorrupt(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"not gzip":  []byte{0x1f, 0x8b, 0, 0, 0},
		"truncated": buildTarGzBytes(t)[:30],
	} {
		if _, _, err := ExtractTarGz(writeFile(t, "c.tar.gz", data), dir, 1<<20); !errors.Is(err, ErrCorruptArchive) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func buildTarGzBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(buildTarGz(t, reg("app", bytes.Repeat([]byte("abcdef"), 100))))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
