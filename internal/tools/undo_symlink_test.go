package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/snap"
)

func TestUndoSymlinkSafety(t *testing.T) {
	dir := t.TempDir()
	root, _ := NewRoot(dir)
	sh, _ := snap.New(root.Dir())
	reg := NewRegistry(root, sh)

	target := filepath.Join(dir, "target.txt")
	os.WriteFile(target, []byte("original"), 0644)

	// internal symlink
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink("target.txt", link); err != nil {
		t.Skip("symlinks not supported")
	}

	// Test TOCTOU scenario limitation: OS-level TOCTOU remains out-of-scope until openat2.
	// The substitution occurs *after* recording the file edit, simulating a user changing the target later.
	filePath := filepath.Join(dir, "file.txt")
	os.WriteFile(filePath, []byte("regular"), 0644)

	ctx := context.Background()
	raw, _ := json.Marshal(map[string]any{"path": "file.txt", "content": "edited"})
	reg.Run(ctx, providerToolCall("write_file", raw))
	rec := reg.LastEdit()

	// 2. User deletes file.txt and makes it a symlink outside the root!
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "escape.txt")
	os.WriteFile(outsideFile, []byte("safe"), 0644)

	os.Remove(filePath)
	os.Symlink(outsideFile, filePath)

	// 3. Undo should fail because the target changed (HashAfter won't match, or it's a symlink).
	res := reg.PersistedUndo([]*agent.EditRecord{rec}, 1)
	if res[0].OK {
		t.Errorf("expected undo to fail when replacing a symlink, but it succeeded")
	}

	// Even if we force it (e.g., hash matches by some miracle), `Resolve` will evaluate the symlink
	// and reject it because it escapes the root!
	// Let's test `Resolve` directly on an escaping symlink.
	_, err := root.Resolve("file.txt")
	if err == nil {
		t.Errorf("expected Resolve to fail on escaping symlink")
	}
}

func TestUndoInternalSymlinkNotReplaced(t *testing.T) {
	dir := t.TempDir()
	root, _ := NewRoot(dir)
	sh, _ := snap.New(root.Dir())
	reg := NewRegistry(root, sh)

	target := filepath.Join(dir, "target.txt")
	os.WriteFile(target, []byte("target"), 0644)

	filePath := filepath.Join(dir, "file.txt")
	os.WriteFile(filePath, []byte("file"), 0644)

	ctx := context.Background()
	raw, _ := json.Marshal(map[string]any{"path": "file.txt", "content": "edited"})
	reg.Run(ctx, providerToolCall("write_file", raw))
	rec := reg.LastEdit()

	// Change to internal symlink
	os.Remove(filePath)
	os.Symlink("target.txt", filePath)

	// Undo should refuse because HashAfter doesn't match (target.txt hash != edited hash, or Capture refuses symlink)
	res := reg.PersistedUndo([]*agent.EditRecord{rec}, 1)
	if res[0].OK {
		t.Errorf("expected undo to fail when file became an internal symlink")
	}
}
