package apps

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"runova/internal/artifact"
	"runova/internal/store"
)

const (
	dirMode      = 0o700
	binaryMode   = 0o755
	jarMode      = 0o644
	userIDOffset = 100000
)

// containerUID is the Linux user (and group) an owner's containers run as.
func containerUID(u store.User) int { return userIDOffset + int(u.ID) }

func (s *Service) artifactsRoot(username, app string) string {
	return filepath.Join(s.DataDir, "artifacts", username, app)
}

func (s *Service) artifactPath(username, app string, number int, file string) string {
	return filepath.Join(s.artifactsRoot(username, app), strconv.Itoa(number), file)
}

func (s *Service) appDataDir(username, app string) string {
	return filepath.Join(s.DataDir, "data", username, app)
}

// OSChown hands path to uid:uid; it is the production Service.Chown and needs CAP_CHOWN.
func OSChown(path string, uid int) error { return os.Chown(path, uid, uid) }

// placeArtifact moves src to dst (creating its directory) and sets the mode
// the container needs: executable for binaries, readable for JARs.
func placeArtifact(src, dst string, kind artifact.Kind) error {
	if err := os.MkdirAll(filepath.Dir(dst), dirMode); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("store artifact: %w", err)
	}
	mode := os.FileMode(binaryMode)
	if kind == artifact.KindJar {
		mode = jarMode
	}
	if err := os.Chmod(dst, mode); err != nil {
		return fmt.Errorf("chmod artifact: %w", err)
	}
	return nil
}

// ensureDataDir creates the app's persistent folder owned by the user's UID.
func (s *Service) ensureDataDir(owner store.User, app string) error {
	dir := s.appDataDir(owner.Username, app)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := s.chown()(dir, containerUID(owner)); err != nil {
		return fmt.Errorf("chown data dir: %w", err)
	}
	return nil
}

// removeArtifacts deletes the files of removed deployments that no remaining
// deployment of the same app still points at (rollbacks share the old file),
// then their directories when they became empty.
func (s *Service) removeArtifacts(removed, remaining []store.Deployment) {
	inUse := make(map[string]bool, len(remaining))
	for _, d := range remaining {
		inUse[d.ArtifactPath] = true
	}
	for _, d := range removed {
		if d.ArtifactPath == "" || inUse[d.ArtifactPath] {
			continue
		}
		if err := os.Remove(d.ArtifactPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("remove artifact", "path", d.ArtifactPath, "err", err)
			continue
		}
		// Fails harmlessly while the directory still holds something.
		os.Remove(filepath.Dir(d.ArtifactPath))
	}
}
