package apps

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errCrossDevice = errors.New("cross-device move")

// renameNoReplace moves oldName in oldDir to newName in newDir without ever
// replacing an existing entry (RENAME_NOREPLACE). Both folders are already
// open inside the workspace and the names have no slash, so no link on the
// way is followed.
func renameNoReplace(oldDir *os.File, oldName string, newDir *os.File, newName string) error {
	err := unix.Renameat2(int(oldDir.Fd()), oldName, int(newDir.Fd()), newName, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return os.ErrExist
	case errors.Is(err, unix.EXDEV):
		return errCrossDevice
	case errors.Is(err, unix.ENOENT):
		return os.ErrNotExist
	}
	return &os.LinkError{Op: "rename", Old: oldName, New: newName, Err: err}
}
