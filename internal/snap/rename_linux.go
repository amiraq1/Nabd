//go:build linux || android

package snap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func renameNoReplace(oldpath, newpath string) error {
	return classifyRenameat2Error(unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE))
}

// classifyRenameat2Error maps kernel/filesystem-level renameat2 outcomes to
// snap-level errors. ENOSYS (syscall absent) and EINVAL (flag unsupported)
// mean the platform cannot provide atomic no-replace publication; this is a
// runtime capability outcome, never a reason to fall back to a plain
// replacing os.Rename. EEXIST is the expected no-replace outcome and is left
// untouched so the caller can route into blob verification.
func classifyRenameat2Error(err error) error {
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
			return ErrAtomicPublishUnsupported
		}
		// In Go, os.IsExist recognizes syscall.EEXIST, which renameat2 returns
		return err
	}
	return nil
}

// probeNoReplaceSupport verifies at runtime that the filesystem backing dir
// actually honors RENAME_NOREPLACE. This is a real syscall probe executed on
// the destination filesystem, not an assumption derived from a successful
// build on Linux/amd64. It returns nil when the flag is honored, and
// ErrAtomicPublishUnsupported (or a wrapping error) otherwise.
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
		if errors.Is(err, ErrAtomicPublishUnsupported) {
			return err
		}
		if os.IsExist(err) {
			// Destination exists and was NOT replaced: RENAME_NOREPLACE honored.
			return nil
		}
		return fmt.Errorf("capability probe: %w", err)
	}
	// renameNoReplace succeeded while dst existed: the flag was silently
	// ignored and the destination was replaced. Never publish on such a
	// filesystem.
	return fmt.Errorf("%w: RENAME_NOREPLACE flag ignored by filesystem", ErrAtomicPublishUnsupported)
}
