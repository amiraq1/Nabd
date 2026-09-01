package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/provider"
	"nabd/internal/snap"
)

func newReg(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	return NewRegistry(root, sh), root.Dir()
}

func run(t *testing.T, r *Registry, tool string, args map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(args)
	if _, _, err := r.Run(context.Background(), providerToolCall(tool, raw)); err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
}

// providerToolCall converts our internal args into a provider.ToolCall
func providerToolCall(name string, input json.RawMessage) provider.ToolCall {
	return provider.ToolCall{
		ID:    "test-id",
		Name:  name,
		Input: input,
	}
}
func TestUndoDeletesCreatedFile(t *testing.T) {
	r, dir := newReg(t)
	run(t, r, "write_file", map[string]any{"path": "a.txt", "content": "hi\n"})
	if res := r.Undo(1); !res[0].OK {
		t.Fatalf("refused: %v", res[0].Note)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("الملف بقي بعد التراجع")
	}
}

func TestUndoRestoresContentAndOrder(t *testing.T) {
	r, dir := newReg(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	run(t, r, "edit_file", map[string]any{"path": "a.txt", "old": "one", "new": "two"})
	run(t, r, "write_file", map[string]any{"path": "b.txt", "content": "x\n"})

	r.Undo(1) // newest first: b.txt
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("التراجع بدأ من الطرف الخطأ")
	}
	r.Undo(1)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\n" {
		t.Fatalf("المحتوى %q", b)
	}
	if len(r.Pending()) != 0 {
		t.Fatal("السجل لم يفرغ")
	}
}

func TestUndoRefusesAfterHumanEdit(t *testing.T) {
	r, dir := newReg(t)
	run(t, r, "write_file", map[string]any{"path": "a.txt", "content": "agent\n"})
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("human\n"), 0o644)

	res := r.Undo(1)
	if res[0].OK {
		t.Fatal("دهس عمل الإنسان")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "human\n" {
		t.Fatalf("المحتوى تغيّر رغم الرفض: %q", b)
	}
	if len(r.Pending()) != 1 {
		t.Fatal("القيد المرفوض سقط من السجل")
	}
}

func TestUndoStopsAtFirstRefusal(t *testing.T) {
	r, dir := newReg(t)
	run(t, r, "write_file", map[string]any{"path": "a.txt", "content": "1\n"})
	run(t, r, "write_file", map[string]any{"path": "b.txt", "content": "2\n"})
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("touched\n"), 0o644)

	res := r.Undo(2)
	if len(res) != 1 || res[0].OK {
		t.Fatalf("تابع بعد الرفض: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal("تراجع عن قيد خلف قيد مرفوض")
	}
}
