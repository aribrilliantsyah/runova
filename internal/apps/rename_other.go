//go:build !linux

package apps

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errCrossDevice = errors.New("cross-device move")

// renameNoReplace is the development fallback for systems without
// RENAME_NOREPLACE: it checks the target is free, then renames. Runova
// servers run Linux, where the check and the move are one atomic call.
func renameNoReplace(oldDir *os.File, oldName string, newDir *os.File, newName string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(int(newDir.Fd()), newName, &st, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		return os.ErrExist
	}
	err := unix.Renameat(int(oldDir.Fd()), oldName, int(newDir.Fd()), newName)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EXDEV):
		return errCrossDevice
	case errors.Is(err, unix.ENOENT):
		return os.ErrNotExist
	}
	return &os.LinkError{Op: "rename", Old: oldName, New: newName, Err: err}
}
