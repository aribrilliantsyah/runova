package apps

import (
	"context"
	"errors"

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

// dbEnv returns the database variables of the app; nil when DBEnv is unset or the app uses no database.
func (s *Service) dbEnv(ctx context.Context, app store.App) ([]string, error) {
	if s.DBEnv == nil || app.DatabaseID == nil {
		return nil, nil
	}
	return s.DBEnv(ctx, app)
}

func sameID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
