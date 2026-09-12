//go:build !android

package tools

import (
	"os"

	"nabd/internal/snap"
)

// captureFromRoot is the COMPATIBILITY PATH used on platforms where the
// descriptor-relative operations in internal/safefs are unavailable.
//
// On these platforms absolute is the compatibility path: it is the
// Root.Resolve-derived path produced by the caller, and state is captured by
// reading it by name. That is the resolve-then-open pattern this project is
// removing on Android, so this carries no new TOCTOU guarantee. New
// confinement-sensitive code must not rely on it.
func captureFromRoot(sh *snap.Shadow, root *Root, relative, absolute string) (snap.State, error) {
	return sh.Capture(absolute)
}

// writeFromRoot is the COMPATIBILITY PATH for non-Android platforms: it creates
// the missing parent directories with the inherited mode, then publishes
// atomically by absolute path. It is not a safe fallback and carries no new
// TOCTOU guarantee.
func writeFromRoot(root *Root, relative, absolute string, data []byte, mode os.FileMode) error {
	if err := mkdirParentDirs(absolute); err != nil {
		return err
	}
	return snap.WriteAtomic(absolute, data, mode)
}
