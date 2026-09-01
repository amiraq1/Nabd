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

// TestPersistedUndoAcrossRestart proves STEP 2: an edit survives a process
// restart and can be undone from the journal records alone — the in-memory
// edit log is deliberately NOT used. It also proves the HashAfter guard
// (external modification is denied) and that a second undo is a no-op.
func TestPersistedUndoAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}

	// --- SESSION A: read → write ---
	regA := NewRegistry(root, sh)
	ctx := context.Background()

	path := filepath.Join(dir, "doc.md")
	os.WriteFile(path, []byte("أصل\n"), 0o644)

	// PRE_EDIT_HASH: the file's fingerprint before the agent touches it.
	preEditHash := sha256OfFile(t, path)

	raw, _ := json.Marshal(map[string]any{"path": "doc.md"})
	if _, ok, err := regA.Run(ctx, providerToolCall("read_file", raw)); err != nil || !ok {
		t.Fatalf("read_file: ok=%v err=%v", ok, err)
	}
	raw, _ = json.Marshal(map[string]any{"path": "doc.md", "content": "أصل\nمعدّل\n"})
	if _, ok, err := regA.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}

	recA := regA.LastEdit()
	if recA == nil {
		t.Fatal("no edit record in session A")
	}

	// The journal would persist recA. Simulate the restart: a brand-new
	// Registry with the SAME shadow (content store persists) and only the
	// record that came from the journal — no in-memory editLog.
	regB := NewRegistry(root, sh)
	// regB must NOT know about the edit through its in-memory log.
	if got := len(regB.Edits()); got != 0 {
		t.Fatalf("fresh registry has %d in-memory edits, want 0", got)
	}

	// --- /undo from journal records only ---
	res := regB.PersistedUndo([]*agent.EditRecord{recA}, 1)
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("persisted undo failed: %+v", res)
	}

	postUndoHash := sha256OfFile(t, path)
	// PRE_EDIT_HASH == POST_UNDO_HASH: the file is back to its original.
	if preEditHash != postUndoHash {
		t.Errorf("undo did not restore original: pre=%s post=%s", preEditHash, postUndoHash)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "أصل\n" {
		t.Errorf("content after undo: %q, want %q", string(b), "أصل\n")
	}

	// --- second undo: must be a no-op, not a duplicate revert ---
	res2 := regB.PersistedUndo([]*agent.EditRecord{recA}, 1)
	if len(res2) != 1 {
		t.Fatalf("second undo returned %d results, want 1", len(res2))
	}
	// The file now has original content; HashAfter ("أصل\nمعدّل\n") no longer
	// matches, so the second undo must refuse — never write over the file.
	if res2[0].OK {
		t.Errorf("second undo succeeded, want refusal (duplicate revert)")
	}
	if got := sha256OfFile(t, path); got != preEditHash {
		t.Errorf("second undo changed the file: %s", got)
	}

	// --- external modification → denied ---
	os.WriteFile(path, []byte("يدي غيرتها\n"), 0o644)
	recC := regA.LastEdit() // reuse a real record
	res3 := regB.PersistedUndo([]*agent.EditRecord{recC}, 1)
	if res3[0].OK {
		t.Errorf("undo overwrote an externally modified file, want denial")
	}
	if got := sha256OfFile(t, path); got != sha256OfFileBytes(t, []byte("يدي غيرتها\n")) {
		t.Errorf("externally modified file was touched by the refused undo")
	}
}

func sha256OfFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256OfFileBytes(t, b)
}

func sha256OfFileBytes(t *testing.T, b []byte) string {
	t.Helper()
	return sha256hex(b)
}
