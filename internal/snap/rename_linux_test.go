//go:build linux || android

package snap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// TestClassifyRenameat2Error pins the error taxonomy that keeps snapshot
// publication from silently falling back to a replacing rename: ENOSYS and
// EINVAL (platform cannot do no-replace) become ErrAtomicPublishUnsupported,
// while EEXIST (the expected no-replace outcome) passes through untouched
// for blob verification.
func TestClassifyRenameat2Error(t *testing.T) {
	if err := classifyRenameat2Error(nil); err != nil {
		t.Fatalf("nil -> %v, want nil", err)
	}
	for _, platformErr := range []error{unix.ENOSYS, unix.EINVAL} {
		if err := classifyRenameat2Error(platformErr); !errors.Is(err, ErrAtomicPublishUnsupported) {
			t.Fatalf("%v -> %v, want ErrAtomicPublishUnsupported", platformErr, err)
		}
	}
	if err := classifyRenameat2Error(unix.EEXIST); !os.IsExist(err) {
		t.Fatalf("EEXIST -> %v, want an os.IsExist error", err)
	} else if errors.Is(err, ErrAtomicPublishUnsupported) {
		t.Fatalf("EEXIST must not map to ErrAtomicPublishUnsupported: %v", err)
	}
	other := unix.EACCES
	if err := classifyRenameat2Error(other); !errors.Is(err, other) {
		t.Fatalf("EACCES -> %v, want it passed through", err)
	}
}

// TestRenameNoReplace exercises the real syscall: renaming onto an existing
// destination must fail with EEXIST and leave the destination bytes
// untouched — this is the data-loss surface the snapshot/undo path depends
// on.
func TestRenameNoReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := renameNoReplace(src, dst); !os.IsExist(err) {
		t.Fatalf("rename onto existing: err=%v, want EEXIST", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "precious" {
		t.Fatalf("destination was replaced: %q", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source should survive a failed no-replace rename: %v", err)
	}

	free := filepath.Join(dir, "free")
	if err := renameNoReplace(src, free); err != nil {
		t.Fatalf("rename to free name: %v", err)
	}
	if got, _ := os.ReadFile(free); string(got) != "new" {
		t.Fatalf("renamed content = %q, want %q", got, "new")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source should be gone after a successful rename")
	}
}

// TestProbeNoReplaceSupport runs the real capability probe against the test
// filesystem. A failure here is a genuine platform signal (the snapshot
// layer refuses to publish where no-replace is not honored), not a test
// environment quirk.
func TestProbeNoReplaceSupport(t *testing.T) {
	if err := probeNoReplaceSupport(t.TempDir()); err != nil {
		t.Fatalf("capability probe failed on the test filesystem: %v", err)
	}
	if err := probeNoReplaceSupport(filepath.Join(t.TempDir(), "does", "not", "exist")); err != nil {
		t.Fatalf("probe should create its directory: %v", err)
	}
}
