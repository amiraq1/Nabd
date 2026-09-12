//go:build unix

package safefs

import (
	"os"

	"golang.org/x/sys/unix"
)

// unlinkat is a test seam. Production always uses the real syscall.
var unlinkat = unix.Unlinkat

// RemoveFile deletes relativeFile beneath rootPath through a descriptor-relative
// unlinkat: the parent directory is opened once (never created), then the base
// name is unlinked relative to that descriptor, so a concurrent rename cannot
// redirect the deletion to another path.
//
// A missing target is reported as os.ErrNotExist, matching the standard
// library's removal semantics. A symlink target is removed as a link (unlinkat
// does not follow it), never its referent.
//
// This implementation is unix-only; other platforms return
// ErrUnsupportedPlatform (remove_other.go).
func RemoveFile(rootPath, relativeFile string) error {
	rel, parent, base, err := splitFileTarget(relativeFile)
	if err != nil {
		return err
	}

	// Open the parent without creating it: removal must never create a
	// directory as a side effect.
	dir, err := OpenDir(rootPath, parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	fd := int(dir.Fd())

	if err := unlinkat(fd, base, 0); err != nil {
		return &os.PathError{Op: "unlinkat", Path: rel, Err: err}
	}
	// Make the deletion durable: a crash right after must not resurrect the
	// file from the directory's old state.
	if err := fsyncFD(fd); err != nil {
		return &os.PathError{Op: "fsync", Path: rel, Err: err}
	}
	return nil
}
