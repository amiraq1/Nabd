//go:build !linux && !android && !windows

package snap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func renameNoReplace(oldpath, newpath string) error {
	err := os.Link(oldpath, newpath)
	if err != nil {
		if os.IsExist(err) {
			return err
		}
		// Any other link error (permission denied, cross-device, not supported)
		// means atomic publish via link is unsupported.
		return errors.Join(ErrAtomicPublishUnsupported, err)
	}
	os.Remove(oldpath)
	return nil
}

// probeNoReplaceSupport verifies at runtime that hard-link publication fails
// on an existing destination (the no-replace contract on these platforms).
func probeNoReplaceSupport(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("capability probe mkdir: %w", err)
	}
	probeDir, err := os.MkdirTemp(dir, ".ag-cap-*")
	if err != nil {
		return fmt.Errorf("capability probe tempdir: %w", err)
	}
	defer os.RemoveAll(probeDir)

	src := filepath.Join(probeDir, "src")
	dst := filepath.Join(probeDir, "dst")
	if err := os.WriteFile(src, []byte("probe"), 0o600); err != nil {
		return fmt.Errorf("capability probe write src: %w", err)
	}
	if err := os.WriteFile(dst, []byte("probe"), 0o600); err != nil {
		return fmt.Errorf("capability probe write dst: %w", err)
	}

	err = os.Link(src, dst)
	if err != nil {
		if os.IsExist(err) {
			// Destination exists and was NOT replaced: no-replace honored.
			return nil
		}
		return errors.Join(ErrAtomicPublishUnsupported, fmt.Errorf("capability probe: %w", err))
	}
	// os.Link cannot succeed over an existing destination on POSIX semantics,
	// so reaching here means the platform replaced the destination.
	return fmt.Errorf("%w: link replaced existing destination", ErrAtomicPublishUnsupported)
}
