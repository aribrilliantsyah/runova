package apps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"runova/internal/store"
)

// DeleteAllForUser removes everything Runova holds for owner outside the
// database rows: every app (containers, artifacts, data), the user's folders
// and their Docker network. actor is whoever asked for it. The first failure
// stops the cleanup and is returned, so the caller keeps the user's row and
// can retry. An app that is being deployed makes it fail with ErrBusy.
func (s *Service) DeleteAllForUser(ctx context.Context, actor, owner store.User) error {
	list, err := s.Store.AppsByUser(ctx, owner.ID)
	if err != nil {
		return err
	}
	for _, app := range list {
		if err := s.Delete(ctx, actor, app.ID); err != nil {
			return fmt.Errorf("delete app %s: %w", app.Name, err)
		}
	}
	for _, sub := range []string{"artifacts", "data", "mounts"} {
		dir := filepath.Join(s.DataDir, sub, owner.Username)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	if err := s.Engine.RemoveNetwork(ctx, networkName(owner.ID)); err != nil {
		return fmt.Errorf("remove network: %w", err)
	}
	return nil
}
