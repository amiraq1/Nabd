//go:build windows

package snap

import (
	"syscall"
)

func renameNoReplace(oldpath, newpath string) error {
	from, err := syscall.UTF16PtrFromString(oldpath)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(newpath)
	if err != nil {
		return err
	}
	return syscall.MoveFile(from, to)
}
