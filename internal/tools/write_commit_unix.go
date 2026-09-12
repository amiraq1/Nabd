//go:build unix

package tools

import (
	"errors"
	"io"
	"os"

	"nabd/internal/safefs"
	"nabd/internal/snap"
)

// captureFromRoot returns the current state of a file beneath root, reading it
// through a descriptor-relative open (safefs.OpenRead).
//
// relative is the filesystem authority: only it reaches the descriptor walk.
// absolute is reporting metadata (the Rel and blob are built from it) and is
// never used for a filesystem operation. A missing target — an ENOENT from the
// descriptor walk — becomes an absent state via CaptureAbsent, not an error.
func captureFromRoot(sh *snap.Shadow, root *Root, relative, absolute string) (snap.State, error) {
	f, err := safefs.OpenRead(root.Dir(), relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sh.CaptureAbsent(absolute)
		}
		return snap.State{}, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return snap.State{}, err
	}
	fi, err := f.Stat()
	if err != nil {
		return snap.State{}, err
	}
	return sh.CaptureBytes(absolute, data, fi.Mode().Perm())
}

// writeFromRoot publishes data to relative beneath root atomically, through
// descriptor-relative operations (safefs.WriteFileAtomic): a temporary file in
// the parent descriptor, fsynced and renamed over the target, then the parent
// fsynced for durability.
//
// relative is the filesystem authority; absolute is reporting metadata and is
// never used for a filesystem operation.
func writeFromRoot(root *Root, relative, absolute string, data []byte, mode os.FileMode) error {
	return safefs.WriteFileAtomic(root.Dir(), relative, data, mode)
}
