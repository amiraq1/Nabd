//go:build !unix

package tools

import "os"

// removeFromRoot is the COMPATIBILITY PATH used on platforms where the
// descriptor-relative removal in internal/safefs is unavailable. It deletes by
// absolute path and carries no new TOCTOU guarantee; new confinement-sensitive
// code must not rely on it.
func removeFromRoot(root *Root, relative, absolute string) error {
	return os.Remove(absolute)
}
