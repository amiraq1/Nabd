//go:build !windows

package mcp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// readSecureFile mirrors internal/config's openConfigFile/validateOpenedFile:
// O_NOFOLLOW open (no symlink races), regular file, 0600-or-stricter,
// owner check (fail-closed), and a hard size cap. It is intentionally
// duplicated here rather than imported so internal/mcp stays hermetic
// (ADR-0003 §12). Pure standard library: syscall carries O_NOFOLLOW,
// O_CLOEXEC and O_NONBLOCK on unix platforms.
func readSecureFile(path string, limit int64) ([]byte, error) {
	// O_NONBLOCK keeps a hostile FIFO/device from blocking before the
	// descriptor can be verified as a regular file.
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: regular file required", path)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("%s: permissions %04o are open to others; run chmod 600 %s", path, mode, path)
	}
	if err := checkFileOwner(path, fi); err != nil {
		return nil, err
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s: file exceeds %d bytes", path, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: file exceeds %d bytes", path, limit)
	}
	return data, nil
}

// checkFileOwner is fail-closed: if the owner cannot be determined, the file
// is refused rather than trusted.
func checkFileOwner(p string, fi os.FileInfo) error {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot determine file owner; refusing", p)
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s: not owned by current user", p)
	}
	return nil
}

// writeSecureFile atomically replaces path with data: it writes to a fresh
// 0600 temp file in the same directory (O_CREAT|O_EXCL|O_NOFOLLOW, so no
// symlink race) and renames over the target. The rename is atomic on POSIX;
// a crash mid-write never leaves a half-written store behind.
func writeSecureFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// The temp file must not be a symlink either; CreateTemp with O_EXCL
	// already guarantees a fresh regular file.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// openSecureAppend opens path for appending (creating it 0600 if absent)
// with O_NOFOLLOW, refusing symlinks, non-regular files, and files open to
// others. The returned *os.File is positioned for appends.
func openSecureAppend(path string) (*os.File, error) {
	fd, err := syscall.Open(path,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_APPEND|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s: regular file required", path)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		f.Close()
		return nil, fmt.Errorf("%s: permissions %04o are open to others; run chmod 600 %s", path, mode, path)
	}
	if err := checkFileOwner(path, fi); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
