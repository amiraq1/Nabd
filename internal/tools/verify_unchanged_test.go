package tools

import (
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/snap"
)

// TestVerifyUnchanged aborts when the file changed between the before-capture
// and the pre-write re-verification, and passes when it did not.
func TestVerifyUnchanged(t *testing.T) {
	dir := t.TempDir()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := captureFromRoot(sh, root, "doc.txt", p)
	if err != nil {
		t.Fatal(err)
	}

	// Unmodified: passes.
	if err := verifyUnchanged(sh, root, "doc.txt", p, before); err != nil {
		t.Fatalf("unmodified file failed verification: %v", err)
	}

	// Externally modified after capture: aborts.
	if err := os.WriteFile(p, []byte("v2-external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyUnchanged(sh, root, "doc.txt", p, before); err == nil {
		t.Fatal("expected verification to fail after external modification")
	}

	// Concurrently created file (was absent at capture): aborts.
	absentBefore, err := captureFromRoot(sh, root, "new.txt", filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !absentBefore.Absent {
		t.Fatal("expected absent before-state")
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyUnchanged(sh, root, "new.txt", filepath.Join(dir, "new.txt"), absentBefore); err == nil {
		t.Fatal("expected verification to fail for concurrently created file")
	}
}
