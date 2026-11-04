package apps

import (
	"context"
	"errors"

	"runova/internal/artifact"
	"runova/internal/store"
)

// checkDatabase requires that the database exists and belongs to owner.
func (s *Service) checkDatabase(ctx context.Context, owner store.User, id int64) error {
	d, err := s.Store.Database(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.UserID != owner.ID) {
		return invalid("Database tidak ditemukan")
	}
	return err
}

// dbEnv returns the database variables of the app; nil when DBEnv is unset or
// the app uses no database. Names the artifact references and names the user
// set are offered to DBEnv, which fills those it recognizes.
func (s *Service) dbEnv(ctx context.Context, app store.App, rep artifact.Report, env []store.EnvEntry) ([]string, error) {
	if s.DBEnv == nil || app.DatabaseID == nil {
		return nil, nil
	}
	names := make([]string, 0, len(rep.Env)+len(env))
	for _, v := range rep.Env {
		names = append(names, v.Name)
	}
	for _, e := range env {
		names = append(names, e.Name)
	}
	return s.DBEnv(ctx, app, names)
}

func sameID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
