//go:build unix

package tools

import (
	"os"

	"nabd/internal/safefs"
)

// openRegularFromRoot opens a root-relative path through the descriptor walk
// and returns a descriptor only when the target is a regular file. Symlinks,
// directories, FIFOs, sockets, and devices are refused by Fstat, so a traversal
// tool can never report the content of whatever a link points at.
//
// relative is the filesystem authority; absolute is reporting metadata and is
// never used for a filesystem operation.
func openRegularFromRoot(root *Root, relative, absolute string) (*os.File, error) {
	return safefs.OpenRead(root.Dir(), relative)
}
