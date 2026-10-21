package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAppendRefusesSymlink verifies O_NOFOLLOW: continuing a session whose
// journal path is a symlink fails closed instead of appending elsewhere.
func TestAppendRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.jsonl")
	if err := os.WriteFile(target, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	j, err := NewJSONLWithOptions(link, Options{})
	if err == nil {
		_ = j.Close()
		t.Fatal("expected symlink append to fail closed")
	}
	t.Logf("symlink refused with: %v", err)
}
