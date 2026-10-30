package pg

import (
	"context"
	"fmt"
)

// Version returns the server version, e.g. "18.1"; it also proves the server answers.
func (a *PgxAdmin) Version(ctx context.Context) (string, error) {
	var v string
	if err := a.pool.QueryRow(ctx, `SHOW server_version`).Scan(&v); err != nil {
		return "", fmt.Errorf("postgres version: %w", err)
	}
	return v, nil
}
