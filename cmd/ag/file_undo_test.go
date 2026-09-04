package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

// recordingSink collects emitted events for assertions.
type recordingSink struct{ evs []agent.Event }

func (s *recordingSink) Emit(e agent.Event) error {
	s.evs = append(s.evs, e)
	return nil
}

// setupUndoFixture builds a loop whose history carries one edit_record
// produced by a real write_file through the registry, plus the registry
// itself. The shadow and root are shared, exactly as in production.
func setupUndoFixture(t *testing.T) (*agent.Loop, *tools.Registry, string, *recordingSink) {
	t.Helper()
	dir := t.TempDir()
	root, err := tools.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(root, sh)

	// Write the original file, then have the agent read + overwrite it so a
	// persisted edit record exists (with shadow blobs and hashes).
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("أصل\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, ok, err := reg.Run(ctx, pc("read_file", map[string]any{"path": "doc.md"})); err != nil || !ok {
		t.Fatalf("read_file: ok=%v err=%v", ok, err)
	}
	if _, ok, err := reg.Run(ctx, pc("write_file", map[string]any{"path": "doc.md", "content": "أصل\nمعدّل\n"})); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}
	rec := reg.LastEdit()
	if rec == nil {
		t.Fatal("no edit record produced")
	}

	// Build a loop whose journal history carries that edit_record as an
	// EventEdit (what the journal would contain after the agent wrote).
	sink := &recordingSink{}
	loop := &agent.Loop{Sink: sink}
	loop.Seed([]agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "s"},
		{Seq: 2, Parent: 1, Type: agent.EventEdit, Edit: rec},
	})
	return loop, reg, path, sink
}

// pc builds a provider.ToolCall from a tool name and JSON-able args.
func pc(name string, args map[string]any) provider.ToolCall {
	raw, _ := json.Marshal(args)
	return provider.ToolCall{Name: name, Input: raw}
}

// TestFileUndoEmitsOneNoticeAndRestores: /undo through the shared fileUndo
// helper restores the file, emits exactly one Notice, and returns "" (the
// Notice is the single visible feedback).
func TestFileUndoEmitsOneNoticeAndRestores(t *testing.T) {
	loop, reg, path, sink := setupUndoFixture(t)

	b, _ := os.ReadFile(path)
	if string(b) != "أصل\nمعدّل\n" {
		t.Fatalf("setup: file = %q, want edited content", b)
	}

	if got := fileUndo(loop, reg, 1); got != "" {
		t.Fatalf("fileUndo with edits = %q, want '' (Notice is the feedback)", got)
	}

	b, _ = os.ReadFile(path)
	if string(b) != "أصل\n" {
		t.Fatalf("after /undo file = %q, want restored original", b)
	}

	// Exactly one Notice was emitted with the undo summary.
	var notices int
	for _, e := range sink.evs {
		if e.Type == agent.Notice {
			notices++
			if !strings.Contains(e.Text, "/undo 1") {
				t.Fatalf("notice text = %q, want /undo summary", e.Text)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("Notice events = %d, want exactly 1", notices)
	}
}

// TestFileUndoReturnsEmptyStatus: the shared helper returns "" when it
// emits a Notice, so the UI status line does not duplicate the feedback.
func TestFileUndoReturnsEmptyStatus(t *testing.T) {
	loop, reg, _, _ := setupUndoFixture(t)
	if got := fileUndo(loop, reg, 1); got != "" {
		t.Fatalf("fileUndo with edits = %q, want ''", got)
	}
}

// TestFileUndoNoRecords: with no edit records, fileUndo says so visibly and
// emits no Notice (nothing to journal).
func TestFileUndoNoRecords(t *testing.T) {
	sink := &recordingSink{}
	loop := &agent.Loop{Sink: sink}
	loop.Seed([]agent.Event{{Seq: 1, Type: agent.RunStart, Text: "s"}})
	dir := t.TempDir()
	root, err := tools.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(root, sh)

	if got := fileUndo(loop, reg, 1); got == "" {
		t.Fatal("fileUndo with no records must return a visible message")
	}
	for _, e := range sink.evs {
		if e.Type == agent.Notice {
			t.Fatal("no-record undo must not emit a Notice")
		}
	}
}

// TestFileUndoNilGuard: a nil loop or registry never panics.
func TestFileUndoNilGuard(t *testing.T) {
	if got := fileUndo(nil, nil, 1); got == "" {
		t.Fatal("nil guard must return a visible message")
	}
}
