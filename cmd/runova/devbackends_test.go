package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"runova/internal/config"
)

func TestFakeBackendsRefusedOnProductionData(t *testing.T) {
	for _, dir := range []string{"/var/lib/runova", "/var/lib/runova/", "/var/lib/../lib/runova"} {
		cfg := config.Config{DataDir: dir, FakeBackends: true}
		if err := checkFakeBackends(cfg); !errors.Is(err, errFakeOnProduction) {
			t.Errorf("DataDir %q: err = %v, want errFakeOnProduction", dir, err)
		}
	}
	if err := checkFakeBackends(config.Config{DataDir: "/tmp/ui-test", FakeBackends: true}); err != nil {
		t.Errorf("temp dir: %v", err)
	}
	if err := checkFakeBackends(config.Config{DataDir: "/var/lib/runova"}); err != nil {
		t.Errorf("real backends on production data must be fine: %v", err)
	}
}

func TestFakeBackendsFlagFromEnv(t *testing.T) {
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == "RUNOVA_FAKE_BACKENDS" {
				return v
			}
			return ""
		}
	}
	if !config.Load(get("1")).FakeBackends {
		t.Error("RUNOVA_FAKE_BACKENDS=1 should enable fakes")
	}
	for _, v := range []string{"", "0", "true"} {
		if config.Load(get(v)).FakeBackends {
			t.Errorf("RUNOVA_FAKE_BACKENDS=%q must not enable fakes", v)
		}
	}
}

func TestFakeBackendsRefusedForAliasesOfProductionData(t *testing.T) {
	real := t.TempDir()
	old := productionData
	productionData = real
	t.Cleanup(func() { productionData = old })

	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(real)
	for name, dir := range map[string]string{"symlink": link, "relative dot": ".", "relative parent": "../" + filepath.Base(real)} {
		if err := checkFakeBackends(config.Config{DataDir: dir, FakeBackends: true}); !errors.Is(err, errFakeOnProduction) {
			t.Errorf("%s (%q): err = %v, want errFakeOnProduction", name, dir, err)
		}
	}
	if err := checkFakeBackends(config.Config{DataDir: t.TempDir(), FakeBackends: true}); err != nil {
		t.Errorf("an unrelated directory must be allowed: %v", err)
	}
}
