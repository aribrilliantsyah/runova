package update

import "testing"

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.3.0", "0.2.0", 1},
		{"0.2.0", "0.3.0", -1},
		{"0.10.0", "0.9.0", 1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.2", "1.0.0-rc.1", 1},
		{"1.0.0-rc.10", "1.0.0-rc.2", 1},
		{"1.0.0-rc.1", "1.0.0-alpha", 1},
		{"1.0.0-rc", "1.0.0-rc.1", -1},
		{"v0.2.0", "0.2.0", 0},
		{"0.0.2-systest", "0.0.2-systest", 0},
	} {
		got, ok := Compare(tc.a, tc.b)
		if !ok || got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d", tc.a, tc.b, got, ok, tc.want)
		}
	}
	for _, bad := range []string{"dev", "1.2", "latest", "", "1.2.3.4", "1.2.3-", "x.y.z"} {
		if _, ok := Compare(bad, "1.0.0"); ok {
			t.Errorf("Compare(%q, ...) parsed", bad)
		}
		if _, ok := Compare("1.0.0", bad); ok {
			t.Errorf("Compare(..., %q) parsed", bad)
		}
	}
}
