package apps_test

import (
	"slices"
	"strings"
	"testing"

	"runova/internal/apps"
)

func TestValidateLabel(t *testing.T) {
	for _, ok := range []string{"cv-budi", "abc", "a1b", "123", strings.Repeat("a", 40)} {
		if err := apps.ValidateLabel(ok); err != nil {
			t.Errorf("ValidateLabel(%q) = %v, want nil", ok, err)
		}
	}
	bad := map[string]string{
		"ab":                    "3",
		"":                      "3",
		strings.Repeat("a", 41): "40",
		"-abc":                  "tanda hubung",
		"abc-":                  "tanda hubung",
		"Abc":                   "huruf kecil",
		"a.b.c":                 "huruf kecil",
		"a b":                   "huruf kecil",
		"a--":                   "tanda hubung",
		"ab--cd":                "--",
		"xn--abc":               "--",
		"abc_def":               "huruf kecil",
		"é-café":                "huruf kecil",
		"abc\n":                 "huruf kecil",
	}
	for in, want := range bad {
		err := apps.ValidateLabel(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateLabel(%q) = %v, want an error mentioning %q", in, err, want)
		}
	}
}

func TestParseReserved(t *testing.T) {
	got, err := apps.ParseReserved("Blog, shop ,blog")
	if err != nil || !slices.Equal(got, []string{"blog", "shop"}) {
		t.Fatalf("ParseReserved = %v, %v", got, err)
	}
	if got, err := apps.ParseReserved(""); err != nil || len(got) != 0 {
		t.Fatalf("empty = %v, %v", got, err)
	}
	if _, err := apps.ParseReserved("blog,x"); err == nil {
		t.Fatal("a reserved name must follow the label rules")
	}
}

func TestReservedLabels(t *testing.T) {
	has := func(l []string, s string) bool { return slices.Contains(l, s) }
	got := apps.ReservedLabels([]string{"blog"}, "runova.porto.ex.id", "porto.ex.id")
	for _, want := range []string{"www", "api", "mail", "smtp", "ftp", "admin", "panel", "runova", "ns1", "ns2", "localhost", "blog"} {
		if !has(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	got = apps.ReservedLabels(nil, "dash.prodak.id", "prodak.id")
	if !has(got, "dash") {
		t.Errorf("the panel's own label under the hosting domain must be reserved: %v", got)
	}
	got = apps.ReservedLabels(nil, "dash.prodak.id", "porto.ex.id")
	if has(got, "dash") {
		t.Errorf("a panel outside the hosting domain reserves nothing extra: %v", got)
	}
	got = apps.ReservedLabels(nil, "a.dash.prodak.id", "prodak.id")
	if has(got, "a") || has(got, "a.dash") {
		t.Errorf("only a panel exactly one label below reserves a label: %v", got)
	}
}

func TestNormalizeHostingName(t *testing.T) {
	for in, want := range map[string]string{
		"porto.devkriyanesia.my.id": "porto.devkriyanesia.my.id",
		" *.Porto.Ex.ID. ":          "porto.ex.id",
	} {
		if got, err := apps.NormalizeHostingName(in); err != nil || got != want {
			t.Errorf("NormalizeHostingName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "*.", "localhost", "1.2.3.4", "a.*.b.id", "*.*.b.id"} {
		if _, err := apps.NormalizeHostingName(in); err == nil {
			t.Errorf("NormalizeHostingName(%q) accepted", in)
		}
	}
}

func TestHostingNesting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []string
		want     string
	}{
		{"a.b.id", []string{"b.id"}, "b.id"},
		{"b.id", []string{"a.b.id"}, "a.b.id"},
		{"c.id", []string{"a.b.id"}, ""},
		{"ab.id", []string{"b.id"}, ""},
		{"b.id", []string{"b.id"}, ""},
	} {
		if got := apps.HostingNesting(tc.name, tc.existing); got != tc.want {
			t.Errorf("HostingNesting(%q, %v) = %q, want %q", tc.name, tc.existing, got, tc.want)
		}
	}
}

func TestLEGroups(t *testing.T) {
	domains := map[int64]string{1: "porto.devkriyanesia.my.id", 2: "dev.devkriyanesia.my.id", 3: "prodak.id"}
	g := apps.LEGroups(domains, map[int64]int{1: 20, 2: 19, 3: 5}, "")
	if g[1].Count != 39 || g[2].Count != 39 || g[1].Level != "" {
		t.Errorf("same registrable domain must share a count: %+v", g)
	}
	if g[3].Count != 5 {
		t.Errorf("prodak.id is a separate group: %+v", g[3])
	}
	g = apps.LEGroups(domains, map[int64]int{1: 40}, "")
	if g[1].Level != apps.LEWarn || g[2].Level != apps.LEWarn {
		t.Errorf("40 must warn: %+v", g)
	}
	g = apps.LEGroups(domains, map[int64]int{1: 25, 2: 25}, "")
	if g[1].Level != apps.LECrit {
		t.Errorf("50 must be critical: %+v", g[1])
	}
	g = apps.LEGroups(domains, map[int64]int{3: 39}, "runova.prodak.id")
	if g[3].Count != 40 || g[3].Level != apps.LEWarn {
		t.Errorf("the panel's certificate counts in its group: %+v", g[3])
	}
	g = apps.LEGroups(domains, map[int64]int{3: 5}, "runova.other.id")
	if g[3].Count != 5 {
		t.Errorf("a panel outside the group adds nothing: %+v", g[3])
	}
}
