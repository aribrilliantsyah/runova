package secret_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runova/internal/secret"
)

func newBox(t *testing.T) *secret.Box {
	t.Helper()
	path := filepath.Join(t.TempDir(), "k")
	if err := secret.GenerateKey(path); err != nil {
		t.Fatal(err)
	}
	b, err := secret.LoadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := newBox(t)
	s1, err := b.Seal([]byte("rahasia"))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := b.Seal([]byte("rahasia"))
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 {
		t.Fatal("two seals of the same plaintext are identical")
	}
	for _, s := range []string{s1, s2} {
		got, err := b.Open(s)
		if err != nil || string(got) != "rahasia" {
			t.Fatalf("Open = %q, %v", got, err)
		}
	}
}

func TestOpenTampered(t *testing.T) {
	b := newBox(t)
	s, _ := b.Seal([]byte("rahasia"))
	i := len(s) / 2
	flip := byte('A')
	if s[i] == 'A' {
		flip = 'B'
	}
	_, err := b.Open(s[:i] + string(flip) + s[i+1:])
	if !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
	if _, err := b.Open("###"); !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("bad base64: err = %v, want ErrDecrypt", err)
	}
	if _, err := b.Open("AAAA"); !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("short input: err = %v, want ErrDecrypt", err)
	}
}

func TestOpenWithOtherKey(t *testing.T) {
	a, b := newBox(t), newBox(t)
	s, _ := a.Seal([]byte("rahasia"))
	if _, err := b.Open(s); !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestGenerateKeyRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")
	if err := secret.GenerateKey(path); err != nil {
		t.Fatal(err)
	}
	if err := secret.GenerateKey(path); err == nil {
		t.Fatal("second GenerateKey succeeded")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 || st.Size() != 32 {
		t.Fatalf("mode %v size %d", st.Mode().Perm(), st.Size())
	}
}

func TestLoadKeyErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.key")
	if _, err := secret.LoadKey(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing: err = %v", err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("LoadKey created a key file")
	}
	short := filepath.Join(dir, "short.key")
	if err := os.WriteFile(short, make([]byte, 31), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := secret.LoadKey(short); err == nil || !strings.Contains(err.Error(), "harus 32 byte") {
		t.Fatalf("short: err = %v", err)
	}
}
