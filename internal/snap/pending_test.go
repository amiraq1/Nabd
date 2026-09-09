package snap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndReadPending(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// Empty read returns empty slice.
	recs, err := ReadPending(root)
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected 0 records, got %d", len(recs))
	}

	// Append a record.
	rec := &PendingRecord{
		Path:      "doc.md",
		PreHash:   "abc",
		PostHash:  "def",
		BeforeKey: "s256:abc",
		AfterKey:  "s256:def",
		Mode:      0o644,
	}
	if err := AppendPending(root, rec); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Read it back.
	recs, err = ReadPending(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Path != "doc.md" {
		t.Errorf("path = %q, want doc.md", recs[0].Path)
	}
	if recs[0].PreHash != "abc" {
		t.Errorf("prehash = %q, want abc", recs[0].PreHash)
	}
	if recs[0].Pid == 0 {
		t.Error("pid should be set automatically")
	}
	if recs[0].Time == "" {
		t.Error("time should be set automatically")
	}
}

func TestDropPending(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// Append 3 records.
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		if err := AppendPending(root, &PendingRecord{Path: p}); err != nil {
			t.Fatalf("append %s: %v", p, err)
		}
	}

	// Drop the newest 1.
	if err := DropPending(root, 1); err != nil {
		t.Fatalf("drop 1: %v", err)
	}
	recs, err := ReadPending(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records after drop, got %d", len(recs))
	}
	// Newest remaining should be b.md.
	if recs[len(recs)-1].Path != "b.md" {
		t.Errorf("newest = %q, want b.md", recs[len(recs)-1].Path)
	}

	// Drop more than exist: file is removed.
	if err := DropPending(root, 10); err != nil {
		t.Fatalf("drop 10: %v", err)
	}
	if _, err := os.Stat(pendingPath(root)); !os.IsNotExist(err) {
		t.Error("pending file should be removed after dropping all")
	}
}

func TestRecoverPending(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// No pending log.
	recs, pid, err := RecoverPending(root)
	if err != nil {
		t.Fatalf("recover empty: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected 0, got %d", len(recs))
	}
	if pid != 0 {
		t.Errorf("pid = %d, want 0", pid)
	}

	// Add a pending record.
	if err := AppendPending(root, &PendingRecord{Path: "orphan.md", Pid: 12345}); err != nil {
		t.Fatalf("append: %v", err)
	}

	recs, pid, err = RecoverPending(root)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1, got %d", len(recs))
	}
	if recs[0].Path != "orphan.md" {
		t.Errorf("path = %q, want orphan.md", recs[0].Path)
	}
	if pid != 12345 {
		t.Errorf("pid = %d, want 12345", pid)
	}
}

func TestHasPending(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	has, err := HasPending(root)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if has {
		t.Error("expected no pending")
	}

	if err := AppendPending(root, &PendingRecord{Path: "a.md"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	has, err = HasPending(root)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if !has {
		t.Error("expected pending")
	}
}
