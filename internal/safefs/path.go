// Package safefs provides descriptor-relative filesystem operations confined
// to an anchored root directory.
package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrEmptyPath    = errors.New("safefs: empty path")
	ErrNULPath      = errors.New("safefs: path contains NUL")
	ErrAbsolutePath = errors.New("safefs: absolute or volume path")
	ErrTraversal    = errors.New("safefs: parent traversal")
)

// Normalize performs lexical validation of a caller-supplied relative path.
//
// Normalize does not access the filesystem, resolve symlinks, or prove that the
// path is beneath a root. That confinement is provided by descriptor-relative
// open operations after normalization.
//
// The path "." is accepted to represent the anchored root directory. Empty
// paths, absolute or volume-qualified paths, NUL bytes, and any explicit ".."
// component are rejected before filepath.Clean is applied.
func Normalize(input string) (string, error) {
	switch {
	case input == "":
		return "", ErrEmptyPath
	case strings.IndexByte(input, 0) >= 0:
		return "", ErrNULPath
	case filepath.IsAbs(input):
		return "", ErrAbsolutePath
	case filepath.VolumeName(input) != "":
		// Reject drive-relative paths such as C:foo as well as volume-qualified
		// absolute paths. On Unix, ordinary names containing ':' have no volume.
		return "", ErrAbsolutePath
	case hasParentComponent(input):
		return "", ErrTraversal
	}

	cleaned := filepath.Clean(input)

	// Defensive postcondition: filepath.Clean must never introduce or retain a
	// traversal component. Keep this check even though the raw input was
	// already inspected.
	if hasParentComponent(cleaned) {
		return "", ErrTraversal
	}
	if filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" {
		return "", ErrAbsolutePath
	}

	return cleaned, nil
}

// hasParentComponent examines path components using the current platform's
// separator rules. os.IsPathSeparator recognizes both accepted separators on
// Windows and the native separator on Unix.
func hasParentComponent(path string) bool {
	componentStart := 0

	for i := 0; i <= len(path); i++ {
		if i != len(path) && !os.IsPathSeparator(path[i]) {
			continue
		}
		if path[componentStart:i] == ".." {
			return true
		}
		componentStart = i + 1
	}

	return false
}
