package registry

import (
	"fmt"
	"io"
	"os"
)

const (
	MaxFileBytes  = 256 << 10 // 256 KB
	MaxValueBytes = 64 << 10  // 64 KB
)

// validateOpenedFile ensures the file is a regular file, owned by the current
// user on Unix, and has permissions with no group or other access (0600).
func validateOpenedFile(p string, fi os.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: regular file required", p)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%s: permissions %04o are open to others; run chmod 600 %s", p, mode, p)
	}
	if err := checkOwner(p, fi); err != nil {
		return err
	}
	if fi.Size() > MaxFileBytes {
		return fmt.Errorf("%s: file exceeds %d bytes", p, MaxFileBytes)
	}
	return nil
}

// readSecureFile opens path with secure descriptor flags, validates permissions
// and ownership, and returns its content bounded by limit.
func readSecureFile(path string, limit int64) ([]byte, error) {
	f, fi, err := openSecureFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := validateOpenedFile(path, fi); err != nil {
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
