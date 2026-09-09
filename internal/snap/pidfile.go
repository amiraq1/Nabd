// Package snap: pidfile.go writes a small companion file next to the
// advisory lock that names the holding pid. This lets a contending process
// report WHO holds the lock (and whether that pid is still alive) instead
// of just saying "busy". The pidfile is written under the same lock as the
// flock so it cannot be read half-written.
package snap

import (
	"fmt"
	"os"
	"path/filepath"
)

// writePidfile writes the current pid next to the lock file. Must be called
// while the lock is held.
func writePidfile(root string) error {
	pidFile := lockPath(root) + ".pid"
	dir := filepath.Dir(pidFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Temp+rename for atomicity, same discipline as the pending log.
	tmp, err := os.CreateTemp(dir, ".pid-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := fmt.Fprintf(tmp, "%d", os.Getpid()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, pidFile)
}
