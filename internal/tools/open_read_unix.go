//go:build unix

package tools

import (
	"os"
	"path/filepath"

	"nabd/internal/safefs"
)

// readPathFromRoot converts a tool-supplied path into the normalized relative
// path used for the descriptor walk, plus the absolute path used for reporting
// and read-credit accounting.
//
// An absolute path already inside root is converted lexically with
// filepath.Rel; an absolute path outside root produces ".." components, which
// Normalize refuses. No symlink resolution happens here: only the descriptor
// walk in the safe open touches the filesystem.
func readPathFromRoot(root *Root, input string) (relative, absolute string, err error) {
	if filepath.IsAbs(input) {
		relative, err = filepath.Rel(root.Dir(), filepath.Clean(input))
		if err != nil {
			return "", "", err
		}
	} else {
		relative = input
	}

	relative, err = safefs.Normalize(relative)
	if err != nil {
		return "", "", err
	}

	absolute = filepath.Join(root.Dir(), relative)
	return relative, absolute, nil
}

// openReadFromRoot opens input beneath root through the descriptor-relative
// safe open and returns the open descriptor plus the absolute reporting path.
//
// abs is reporting/accounting metadata only. The opened descriptor, not this
// string, is the authority for all file reads and metadata checks.
func openReadFromRoot(root *Root, input string) (*os.File, string, error) {
	relative, abs, err := readPathFromRoot(root, input)
	if err != nil {
		return nil, "", err
	}
	f, err := safefs.OpenRead(root.Dir(), relative)
	if err != nil {
		return nil, "", err
	}
	return f, abs, nil
}
