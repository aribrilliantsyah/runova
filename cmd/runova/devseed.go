package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"

	"runova/internal/artifact"
	"runova/internal/auth"
	"runova/internal/config"
	"runova/internal/store"
)

// errSeedNeedsFakes is returned when RUNOVA_DEV_SEED=1 is set without fake backends:
// seeded apps have no containers, so they only make sense next to the fake Docker.
var errSeedNeedsFakes = errors.New("RUNOVA_DEV_SEED=1 needs RUNOVA_FAKE_BACKENDS=1")

// checkDevSeed refuses the development seed outside a fake-backend development run.
func checkDevSeed(cfg config.Config) error {
	switch {
	case !cfg.DevSeed:
		return nil
	case !cfg.FakeBackends:
		return errSeedNeedsFakes
	case onProductionData(cfg.DataDir):
		return errFakeOnProduction
	}
	return nil
}

// seedPassword is the password of every seeded account (development data only).
const seedPassword = "contoh-password-1"

var seedFirst = []string{"andi", "budi", "citra", "dewi", "eko", "fajar", "gita", "hadi", "indah", "joko", "kartika", "lukman", "maya", "nanda", "oki", "putri", "rizki", "sari", "tono", "umar"}
var seedLast = []string{"wijaya", "santoso", "lestari", "pratama", "saputra", "hidayat", "kusuma", "nugroho"}

// devSeed fills a development database with 64 users, some apps and databases, and a
// month of activity, so the list pages can be judged with realistic volume. It runs
// once: a database that already has the first seed user is left alone.
func devSeed(ctx context.Context, st *store.Store, now int64) error {
	if _, _, err := st.UserByUsername(ctx, seedFirst[0]+seedLast[0]); err == nil {
		return nil
	}
	hash, err := auth.HashPassword(seedPassword)
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewPCG(7, 42))
	const day = 86_400_000
	var users []store.User
	for i := range 64 {
		first, last := seedFirst[i%len(seedFirst)], seedLast[(i/len(seedFirst)+i)%len(seedLast)]
		name := first + last
		display := cap1(first) + " " + cap1(last)
		role := store.RoleUser
		if i%9 == 0 {
			role = store.RoleAdmin
		}
		email := ""
		if i%3 != 2 {
			email = name + "@contoh.id"
		}
		u, err := st.CreateUser(ctx, store.NewUser{
			Username: name, DisplayName: display, Email: email, PasswordHash: hash, Role: role,
			Quota: store.Quota{Apps: 2 + i%5, MemoryMB: 1024 * (1 + i%4), Databases: 1 + i%4, PGConnections: 20, DiskMB: 1024 * (1 + i%3)},
		}, now-int64(90-i)*day)
		if err != nil {
			return fmt.Errorf("seed user %s: %w", name, err)
		}
		users = append(users, u)
	}

	port := 20500
	appsOf := map[int64][]string{}
	for i, u := range users {
		for k := range i % 4 {
			port++
			name := []string{"billing", "portal", "worker", "blog"}[k]
			if _, err := st.CreateApp(ctx, store.App{
				UserID: u.ID, Name: name, Kind: artifact.KindStatic, InternalPort: 8080, IPPort: port,
				MemoryMB: 128 * (1 + (i+k*3)%6), Status: store.AppStopped, DeployMode: store.DeployModeStopStart, CreatedAt: now - 30*day,
			}); err != nil {
				return fmt.Errorf("seed app: %w", err)
			}
			appsOf[u.ID] = append(appsOf[u.ID], name)
		}
		for k := range i % 3 {
			if _, err := st.CreateDatabase(ctx, store.Database{UserID: u.ID, Name: fmt.Sprintf("%s_db%d", u.Username, k+1), CreatedAt: now - 20*day}); err != nil {
				return fmt.Errorf("seed database: %w", err)
			}
		}
	}

	ips := []string{"203.0.113.15", "203.0.113.40", "198.51.100.7", "192.0.2.88", "10.20.0.5"}
	appActions := []string{store.ActDeploy, store.ActDeploy, store.ActDeploy, store.ActRestart, store.ActStop, store.ActStart, store.ActEnvUpdate, store.ActTerminal, store.ActRollback, store.ActDomainAdd}
	for n := range 420 {
		u := users[rng.IntN(len(users))]
		// Most entries are recent; a few go back the whole month.
		ago := int64(rng.ExpFloat64() * 3 * day)
		if ago > 30*day {
			ago = int64(rng.IntN(30 * day))
		}
		a := store.Activity{At: now - ago, ActorID: &u.ID, ActorName: u.Username, IP: ips[rng.IntN(len(ips))]}
		apps := appsOf[u.ID]
		switch r := rng.IntN(10); {
		case r < 3:
			a.Action = store.ActLogin
		case r == 3:
			a.Action = store.ActLoginFailed
		case r < 8 && len(apps) > 0:
			a.Action = appActions[rng.IntN(len(appActions))]
			a.Target = apps[rng.IntN(len(apps))]
			switch a.Action {
			case store.ActDeploy:
				a.Detail = fmt.Sprintf("#%d", 1+rng.IntN(12))
			case store.ActRollback:
				v := 2 + rng.IntN(8)
				a.Detail = fmt.Sprintf("#%d → #%d", v-1, v+1)
			case store.ActDomainAdd:
				a.Detail = a.Target + "." + u.Username + ".contoh.id"
			}
		case r == 8:
			a.Action, a.Target = store.ActDBBackup, fmt.Sprintf("%s_db1", u.Username)
		default:
			a.Action, a.Target = store.ActUpload, fmt.Sprintf("app-%d.jar", n%7)
			a.Detail = "jar"
		}
		if u.Role == store.RoleAdmin && rng.IntN(4) == 0 {
			t := users[rng.IntN(len(users))]
			a.Action, a.Target = store.ActQuotaUpdate, t.Username
			a.Detail = fmt.Sprintf("apps=%d memory_mb=%d databases=3 pg_connections=20 disk_mb=1024", 2+rng.IntN(4), 1024*(1+rng.IntN(3)))
		}
		if err := st.Log(ctx, a); err != nil {
			return fmt.Errorf("seed activity: %w", err)
		}
	}
	// Background work without an actor.
	for n := range 12 {
		if err := st.Log(ctx, store.Activity{At: now - int64(n)*day/2 - 3_600_000, ActorName: "", Action: store.ActRestart, Target: "billing"}); err != nil {
			return fmt.Errorf("seed activity: %w", err)
		}
	}
	return nil
}

func cap1(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
