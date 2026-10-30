package pg

import (
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// ClientEnv returns the libpq environment (PGHOST, PGPORT, PGUSER, PGPASSWORD)
// that makes pg_dump and pg_restore connect the way the admin DSN does.
// PGPORT and PGPASSWORD are omitted when the DSN has none.
func ClientEnv(dsn string) ([]string, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	env := []string{"PGHOST=" + cfg.Host, "PGUSER=" + cfg.User}
	if cfg.Port != 0 {
		env = append(env, "PGPORT="+strconv.Itoa(int(cfg.Port)))
	}
	if cfg.Password != "" {
		env = append(env, "PGPASSWORD="+cfg.Password)
	}
	return env, nil
}

// Port returns the TCP port of the DSN; 5432 when the DSN names none.
func Port(dsn string) (int, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return 0, fmt.Errorf("parse postgres dsn: %w", err)
	}
	if cfg.Port == 0 {
		return 5432, nil
	}
	return int(cfg.Port), nil
}
