package pg

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDBName(t *testing.T) {
	got, err := DBName("budi", "billing")
	if err != nil || got != "budi_billing" {
		t.Fatalf("DBName(budi, billing) = %q, %v", got, err)
	}
	got, err = DBName(strings.Repeat("a", 31), strings.Repeat("b", 31))
	if err != nil || len(got) != 63 {
		t.Fatalf("longest valid name = %q (%d), %v", got, len(got), err)
	}

	bad := []struct{ user, suffix string }{
		{"budi", "Billing"},
		{"budi", "1x"},
		{"budi", "a-b"},
		{"budi", ""},
		{"budi", strings.Repeat("b", 32)},
		{"budi", "x y"},
		{"", "billing"},
		{"Budi", "billing"},
		{"bu_di", "billing"},
		{strings.Repeat("a", 32), "b"},
	}
	for _, c := range bad {
		if got, err := DBName(c.user, c.suffix); err == nil {
			t.Errorf("DBName(%q, %q) = %q, want error", c.user, c.suffix, got)
		}
	}
}

func TestValidateDBSuffix(t *testing.T) {
	for _, s := range []string{"a", "billing", "a_b", "x1_2", strings.Repeat("a", 31)} {
		if err := ValidateDBSuffix(s); err != nil {
			t.Errorf("ValidateDBSuffix(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{"", "_a", "1a", "A", "a-b", "a b", `a"b`, "a;b", strings.Repeat("a", 32)} {
		if err := ValidateDBSuffix(s); err == nil {
			t.Errorf("ValidateDBSuffix(%q) = nil, want error", s)
		}
	}
}

func TestQuoteLiteral(t *testing.T) {
	cases := map[string]string{
		`plain`:        `E'plain'`,
		`it's`:         `E'it''s'`,
		`back\slash`:   `E'back\\slash'`,
		`x'; DROP --`:  `E'x''; DROP --'`,
		`\'`:           `E'\\'''`,
		`quote"double`: `E'quote"double'`,
	}
	for in, want := range cases {
		if got := quoteLiteral(in); got != want {
			t.Errorf("quoteLiteral(%q) = %s, want %s", in, got, want)
		}
	}
}

// Validation runs before any connection is used, so an unreachable pool is enough.
func TestRejectsUnsafeArguments(t *testing.T) {
	ctx := context.Background()
	a, err := Open(ctx, "host=/nonexistent user=runova dbname=postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	checks := map[string]error{
		"EnsureRole uppercase":      a.EnsureRole(ctx, "Budi", "secret", 5),
		"EnsureRole injection":      a.EnsureRole(ctx, `budi"; DROP ROLE x; --`, "secret", 5),
		"EnsureRole runova":         a.EnsureRole(ctx, "runova", "secret", 5),
		"EnsureRole postgres":       a.EnsureRole(ctx, "postgres", "secret", 5),
		"EnsureRole empty password": a.EnsureRole(ctx, "budi", "", 5),
		"EnsureRole NUL password":   a.EnsureRole(ctx, "budi", "a\x00b", 5),
		"EnsureRole negative limit": a.EnsureRole(ctx, "budi", "secret", -1),
		"SetConnLimit negative":     a.SetConnLimit(ctx, "budi", -1),
		"SetConnLimit bad role":     a.SetConnLimit(ctx, "bu di", 5),
		"SetPassword bad role":      a.SetPassword(ctx, "runova", "secret"),
		"SetPassword empty":         a.SetPassword(ctx, "budi", ""),
		"DropRole runova":           a.DropRole(ctx, "runova"),
		"DropRole postgres":         a.DropRole(ctx, "postgres"),
		"DropRole bad":              a.DropRole(ctx, "x;y"),
		"CreateDatabase foreign":    a.CreateDatabase(ctx, "andi_app", "budi"),
		"CreateDatabase no prefix":  a.CreateDatabase(ctx, "budi", "budi"),
		"CreateDatabase injection":  a.CreateDatabase(ctx, `budi_a"; DROP DATABASE x; --`, "budi"),
		"CreateDatabase template":   a.CreateDatabase(ctx, "template0", "budi"),
		"DropDatabase postgres":     a.DropDatabase(ctx, "postgres"),
		"DropDatabase template":     a.DropDatabase(ctx, "template1"),
		"DropDatabase injection":    a.DropDatabase(ctx, `x_y" WITH (FORCE); --`),
		"Terminate postgres":        a.TerminateConnections(ctx, "postgres"),
	}
	_, err = a.Databases(ctx, "Budi")
	checks["Databases bad owner"] = err
	for name, err := range checks {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid before any connection", name, err)
		}
	}
}
