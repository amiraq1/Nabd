//go:build !windows

package store

import (
	"os"

	"golang.org/x/sys/unix"
)

// openFileNoFollow opens path without following a final symlink: a symlink at
// path fails closed (ELOOP) instead of redirecting journal writes into an
// attacker-chosen file.
func openFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path,
		flag|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		uint32(perm.Perm()))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openAppendFile opens path for appending, creating it with 0600 when absent.
func openAppendFile(path string) (*os.File, error) {
	return openFileNoFollow(path, unix.O_WRONLY|unix.O_CREAT|unix.O_APPEND, 0o600)
}
