package main

import (
	"context"
	"time"

	"runova/internal/config"
	"runova/internal/store"
	"runova/internal/update"
)

// fakeReleases stands in for GitHub under RUNOVA_FAKE_BACKENDS=1 and always
// reports a newer release, so the panel's update banner can be seen and tested.
type fakeReleases struct{ update.Source }

func (fakeReleases) Latest(context.Context) (string, error) { return "0.3.0", nil }

// newUpdateChecker builds the background release checker of `runova serve`;
// the update_check setting is read on every cycle so the admin can switch it.
func newUpdateChecker(cfg config.Config, st *store.Store) *update.Checker {
	chk := &update.Checker{
		Current: version,
		Source:  update.Source{GitHub: cfg.GitHub, Repo: cfg.Repo},
		Enabled: func(ctx context.Context) bool {
			s, err := st.Settings(ctx)
			return err == nil && s.UpdateCheck
		},
	}
	if cfg.FakeBackends {
		chk.Current = "0.2.0"
		chk.Source = fakeReleases{update.Source{GitHub: cfg.GitHub, Repo: cfg.Repo}}
		chk.FirstDelay = 100 * time.Millisecond
	}
	return chk
}
