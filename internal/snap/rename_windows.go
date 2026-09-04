//go:build windows

package snap

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func renameNoReplace(oldpath, newpath string) error {
	from, err := syscall.UTF16PtrFromString(oldpath)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(newpath)
	if err != nil {
		return err
	}
	// MoveFile without MOVEFILE_REPLACE_EXISTING fails when the destination
	// exists; Go maps both ERROR_FILE_EXISTS and ERROR_ALREADY_EXISTS to
	// os.ErrExist, so os.IsExist handles both.
	return syscall.MoveFile(from, to)
}

// probeNoReplaceSupport verifies at runtime that MoveFile fails on an existing
// destination (the no-replace contract on Windows), instead of assuming it
// from a successful build elsewhere.
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

	err = renameNoReplace(src, dst)
	if err != nil {
		if os.IsExist(err) {
			// Destination exists and was NOT replaced: no-replace honored.
			return nil
		}
		return fmt.Errorf("capability probe: %w", err)
	}
	// MoveFile succeeded while dst existed: the destination was replaced.
	return fmt.Errorf("%w: MoveFile replaced existing destination", ErrAtomicPublishUnsupported)
}
