package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/snap"
)

// TestUndoStillWorksWithShieldedGitignore: the shield's .gitignore is not an
// edit target and not a shadow object — writing it must not disturb the
// journal-backed undo it protects. After the first write creates the store,
// an edit is recorded and reverted exactly as before, and the shield file is
// untouched by the undo.
func TestUndoStillWorksWithShieldedGitignore(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err == nil {
		if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %s", out)
		}
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(root, sh)
	ctx := context.Background()

	raw1, _ := json.Marshal(map[string]any{"path": "doc.md", "content": "v1\n"})
	if _, ok, err := reg.Run(ctx, providerToolCall("write_file", raw1)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}
	recCreate := reg.LastEdit()
	raw2, _ := json.Marshal(map[string]any{"path": "doc.md", "old": "v1\n", "new": "v2\n"})
	if _, ok, err := reg.Run(ctx, providerToolCall("edit_file", raw2)); err != nil || !ok {
		t.Fatalf("edit_file: ok=%v err=%v", ok, err)
	}
	recEdit := reg.LastEdit()
	if recCreate == nil || recEdit == nil {
		t.Fatal("expected two edit records")
	}

	igPath := filepath.Join(dir, ".ag", ".gitignore")
	if content, err := os.ReadFile(igPath); err != nil {
		t.Fatalf("shield .gitignore missing: %v", err)
	} else if got := strings.TrimSpace(string(content)); got != "*" {
		t.Fatalf(".gitignore = %q, want the single rule *", got)
	}

	results := reg.PersistedUndo([]*agent.EditRecord{recEdit}, 1)
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("undo failed: %+v", results)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "doc.md")); string(b) != "v1\n" {
		t.Fatalf("after undo doc.md = %q, want v1", b)
	}
	if b, _ := os.ReadFile(igPath); strings.TrimSpace(string(b)) != "*" {
		t.Fatalf("undo touched the shield file: %q", b)
	}
}
