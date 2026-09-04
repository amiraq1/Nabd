package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
)

// TestEditRecordEmitted verifies STEP 1 of P0-2: a read → write cycle
// produces exactly one EditRecord event carrying SHA-256 hashes, a unified
// patch, the read line count, and the correct path — through the real
// Registry and the real filesystem.
func TestEditRecordEmitted(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	// 1. read_file: 3 lines, must be recorded as ReadLines.
	path := filepath.Join(dir, "notes.md")
	os.WriteFile(path, []byte("سطر واحد\nسطر اثنان\nسطر ثلاثة\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "notes.md"})
	out, err := r.RunDetailed(ctx, "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}
	if out.LinesRead != 3 {
		t.Fatalf("out.LinesRead=%d, want 3 (the read must be recorded)", out.LinesRead)
	}
	// reads are result-scoped; the loop stages the count for the next write.
	r.SetLinesRead(out.LinesRead)

	// 2. write and check the EditRecord. The staged count flows read → write
	// without stale carry-over (the write consumes exactly what the loop staged).
	raw, _ = json.Marshal(map[string]any{
		"path":    "notes.md",
		"content": "سطر واحد\nسطر معدّل\n",
	})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}

	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("LastEdit() = nil, want an EditRecord")
	}
	if rec.Path != "notes.md" {
		t.Errorf("Path=%q, want notes.md", rec.Path)
	}
	if rec.HashBefore == "" {
		t.Error("HashBefore empty, want SHA-256 of the pre-write content")
	}
	if rec.HashAfter == "" {
		t.Error("HashAfter empty, want SHA-256 of the written content")
	}
	if len(rec.HashBefore) != 64 || len(rec.HashAfter) != 64 {
		t.Errorf("hashes not SHA-256 (64 hex): before=%d after=%d", len(rec.HashBefore), len(rec.HashAfter))
	}
	if rec.Patch == "" {
		t.Error("Patch empty, want a unified diff")
	}
	if !strings.Contains(rec.Patch, "--- a/notes.md") || !strings.Contains(rec.Patch, "+++ b/notes.md") {
		t.Errorf("Patch lacks unified diff headers:\n%s", rec.Patch)
	}
	if !strings.Contains(rec.Patch, "-سطر ثلاثة") || !strings.Contains(rec.Patch, "+سطر معدّل") {
		t.Errorf("Patch lacks the actual change:\n%s", rec.Patch)
	}
	if rec.ReadLines != 3 {
		t.Errorf("ReadLines=%d, want 3 (lines the model read before writing)", rec.ReadLines)
	}

	// 3. Exactly one edit recorded.
	if got := len(r.Edits()); got != 1 {
		t.Fatalf("edit count=%d, want 1", got)
	}

	// 4. Filesystem state: the written content is on disk.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "سطر واحد\nسطر معدّل\n" {
		t.Errorf("filesystem content mismatch: %q", string(b))
	}
}

// TestEditRecordCreationHasEmptyHashBefore: creating a brand-new file must
// leave HashBefore empty (there was no "before").
func TestEditRecordCreationHasEmptyHashBefore(t *testing.T) {
	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{
		"path":    "fresh.md",
		"content": "جديد\n",
	})
	if _, ok, err := r.Run(context.Background(), providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}
	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("LastEdit() = nil")
	}
	if rec.HashBefore != "" {
		t.Errorf("HashBefore=%q, want empty for a new file", rec.HashBefore)
	}
	if rec.HashAfter == "" {
		t.Error("HashAfter empty for a new file")
	}
	if rec.ReadLines != 0 {
		t.Errorf("ReadLines=%d, want 0 (blind write, no read before)", rec.ReadLines)
	}
}

// TestEditRecordNotInMessages: Messages() must never carry the patch — the
// model hears summaries, not diffs.
func TestEditRecordNotInMessages(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.EventEdit, Edit: &agent.EditRecord{
			Path: "x.md", HashBefore: "a", HashAfter: "b",
			Patch: "--- a/x.md\n+++ b/x.md\n-secret\n+public\n", ReadLines: 3,
		}},
		{Seq: 2, Type: agent.UserMsg, Text: "بعد التعديل"},
	}
	msgs := agent.Messages(evs)
	for _, m := range msgs {
		if strings.Contains(m.Text, "secret") || strings.Contains(m.Text, "--- a/") {
			t.Fatalf("patch leaked into provider messages: %q", m.Text)
		}
	}
}
