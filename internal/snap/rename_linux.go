//go:build linux || android

package snap

import (
	"errors"
	"golang.org/x/sys/unix"
)

func renameNoReplace(oldpath, newpath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
			return ErrAtomicPublishUnsupported
		}
		// In Go, os.IsExist recognizes syscall.EEXIST, which renameat2 returns
		return err
	}
	return nil
}
