package main

import (
	"errors"
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
