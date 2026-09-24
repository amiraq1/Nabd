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

// legacyStoreFor writes a store that predates the shield: .ag and .ag/shadow
// exist (0700, with one old 0400 blob) and there is no .gitignore yet.
func legacyStoreFor(t *testing.T, dir string) (*Root, *snap.Shadow) {
	t.Helper()
	agDir := filepath.Join(dir, ".ag")
	shadowDir := filepath.Join(agDir, "shadow")
	if err := os.MkdirAll(filepath.Join(shadowDir, "ab"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shadowDir, "ab", "cd"), []byte("old blob"), 0o400); err != nil {
		t.Fatal(err)
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	return root, sh
}

// TestShadowGitignoreAddedToExistingStoreOnEdit: a store that predates the
// shield, or lost its .gitignore, must be shielded by the next edit — "never
// replace an existing file" does not mean "write only when the directory is
// created". One edit, then .ag/.gitignore exists and git status shows no .ag
// path.
func TestShadowGitignoreAddedToExistingStoreOnEdit(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err == nil {
		if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %s", out)
		}
	}
	root, sh := legacyStoreFor(t, dir)
	reg := NewRegistry(root, sh)

	raw, _ := json.Marshal(map[string]any{"path": "doc.md", "content": "v1\n"})
	if _, ok, err := reg.Run(context.Background(), providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}

	igPath := filepath.Join(dir, ".ag", ".gitignore")
	content, err := os.ReadFile(igPath)
	if err != nil {
		t.Fatalf("shield .gitignore not added to the existing store: %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != "*" {
		t.Fatalf(".gitignore = %q, want the single rule *", got)
	}

	if _, err := exec.LookPath("git"); err == nil {
		out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
		if err != nil {
			t.Fatalf("git status: %v", err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.Contains(line, ".ag") {
				t.Errorf("store visible in git status: %q\nfull output:\n%s", line, out)
			}
		}
	}
}

// TestShadowGitignoreRecreatedOnNextWrite: the shield file is asserted on
// every write-open, not once per process. If it disappears mid-session, the
// next edit puts it back.
func TestShadowGitignoreRecreatedOnNextWrite(t *testing.T) {
	dir := t.TempDir()
	root, sh := legacyStoreFor(t, dir)
	reg := NewRegistry(root, sh)
	ctx := context.Background()

	raw1, _ := json.Marshal(map[string]any{"path": "doc.md", "content": "v1\n"})
	if _, ok, err := reg.Run(ctx, providerToolCall("write_file", raw1)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}
	igPath := filepath.Join(dir, ".ag", ".gitignore")
	if _, err := os.Stat(igPath); err != nil {
		t.Fatalf("setup: shield file missing after first write: %v", err)
	}
	if err := os.Remove(igPath); err != nil {
		t.Fatal(err)
	}

	raw2, _ := json.Marshal(map[string]any{"path": "doc.md", "old": "v1\n", "new": "v2\n"})
	if _, ok, err := reg.Run(ctx, providerToolCall("edit_file", raw2)); err != nil || !ok {
		t.Fatalf("edit_file: ok=%v err=%v", ok, err)
	}

	if content, err := os.ReadFile(igPath); err != nil {
		t.Fatalf("shield file not recreated by the next write-open: %v", err)
	} else if got := strings.TrimSpace(string(content)); got != "*" {
		t.Fatalf(".gitignore = %q, want the single rule *", got)
	}
}
