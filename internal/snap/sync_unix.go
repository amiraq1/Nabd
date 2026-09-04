//go:build !windows

package snap

import "os"

// syncDir attempts to fsync the directory to ensure directory entries (like renames)
// are durably persisted to disk.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
