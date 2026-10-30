package main

import (
	"context"
	"log/slog"
	"time"

	"runova/internal/config"
	"runova/internal/dbs"
	"runova/internal/pg"
	"runova/internal/secret"
	"runova/internal/store"
)

const pgStartupWait = 10 * time.Second

// openDatabases builds the database service. An unreachable PostgreSQL is
// not fatal: the database endpoints answer 503 and the rest of Runova works;
// only a DSN that cannot be parsed is an error. When PostgreSQL answers, the
// roles of all users are reconciled and pg_hba.d is written.
func openDatabases(ctx context.Context, cfg config.Config, st *store.Store, box *secret.Box) (*dbs.Service, func(), error) {
	admin, err := pg.Open(ctx, cfg.PGDSN)
	if err != nil {
		return nil, nil, err
	}
	env, err := pg.ClientEnv(cfg.PGDSN)
	if err != nil {
		admin.Close()
		return nil, nil, err
	}
	port, err := pg.Port(cfg.PGDSN)
	if err != nil {
		admin.Close()
		return nil, nil, err
	}
	svc := &dbs.Service{
		Store: st, PG: admin, Box: box, BackupDir: cfg.BackupDir(),
		HBAPath: cfg.PGHBAPath, PGEnv: env, PGPort: port,
	}

	pingCtx, cancel := context.WithTimeout(ctx, pgStartupWait)
	defer cancel()
	if err := admin.Ping(pingCtx); err != nil {
		slog.Warn("PostgreSQL tidak bisa diakses; fitur database nonaktif sampai tersambung", "err", err)
		return svc, admin.Close, nil
	}
	if err := svc.ReconcileRoles(ctx); err != nil {
		slog.Error("reconcile postgres roles", "err", err)
	}
	if err := svc.WriteHBA(ctx); err != nil {
		slog.Error("write pg_hba", "path", cfg.PGHBAPath, "err", err)
	}
	return svc, admin.Close, nil
}
