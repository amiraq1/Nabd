//go:build windows

package snap

// syncDir is a no-op on Windows because directory handles cannot be fsync'd
// in the same way as POSIX systems using os.Open and d.Sync().
// Power-loss durability is limited to the filesystem and platform guarantees;
// this helper provides only best-effort synchronization on this OS.
func syncDir(dir string) error {
	return nil
}
