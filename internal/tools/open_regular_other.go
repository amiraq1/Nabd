//go:build !unix

package tools

import (
	"os"

	"nabd/internal/safefs"
)

// openRegularFromRoot is the COMPATIBILITY PATH used on platforms where the
// descriptor-relative open in internal/safefs is unavailable. It Lstats the
// absolute path, refuses anything that is not a regular file, and only then
// opens it by name. It carries no new TOCTOU guarantee.
func openRegularFromRoot(root *Root, relative, absolute string) (*os.File, error) {
	fi, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, safefs.ErrNotRegular
	}
	return os.Open(absolute)
}
