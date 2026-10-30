package caddy_test

import (
	"strings"
	"testing"

	"runova/internal/caddy"
)

func TestValidateDomain(t *testing.T) {
	ok := map[string]string{
		"App.Contoh.ID.": "app.contoh.id",
		"a-b.co":         "a-b.co",
		" x.co ":         "x.co",
		"1.example.com":  "1.example.com",
	}
	for in, want := range ok {
		got, err := caddy.ValidateDomain(in)
		if err != nil || got != want {
			t.Errorf("ValidateDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 59) + ".co"
	if len(long) != 254 {
		t.Fatalf("setup: name is %d bytes, want 254", len(long))
	}
	bad := []string{
		"", "contoh", "-a.co", "a-.co", "a_b.co", "*.a.co", "203.0.113.5", "2001:db8::1", "a..co", ".a.co",
		long, strings.Repeat("a", 64) + ".co", "a b.co", "a.co/path", "a.co:8080",
	}
	for _, in := range bad {
		if got, err := caddy.ValidateDomain(in); err == nil {
			t.Errorf("ValidateDomain(%q) = %q, want an error", in, got)
		}
	}
}
