//go:build !windows

package snap

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSyncDir covers the durability helper: syncing a real directory
// succeeds, and a missing directory surfaces an error instead of silently
// passing.
func TestSyncDir(t *testing.T) {
	dir := t.TempDir()
	if err := syncDir(dir); err != nil {
		t.Fatalf("syncDir(%q): %v", dir, err)
	}
	missing := filepath.Join(dir, "does-not-exist")
	if err := syncDir(missing); err == nil {
		t.Fatalf("syncDir(%q): want error, got nil", missing)
	}
	// A file is not a directory entry container the caller means, but the
	// syscall itself must not corrupt anything; just observe it errors or
	// succeeds without touching the file.
	f := filepath.Join(dir, "f")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = syncDir(f)
	if got, _ := os.ReadFile(f); string(got) != "x" {
		t.Fatal("syncDir must not modify the target")
	}
}
