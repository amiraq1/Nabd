//go:build windows

package snap

// syncDir is a no-op on Windows because directory handles cannot be fsync'd
// in the same way as POSIX systems using os.Open and d.Sync().
// While NTFS provides journals that often preserve renames effectively,
// full explicit directory-entry power-loss durability is not guaranteed here.
func syncDir(dir string) error {
	return nil
}
