//go:build !windows

package registry

import (
	"os"

	"golang.org/x/sys/unix"
)

func openSecureFile(path string) (*os.File, os.FileInfo, error) {
	// O_NONBLOCK prevents a hostile FIFO or device path from blocking before
	// validateOpenedFile can verify the descriptor refers to a regular file.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}
