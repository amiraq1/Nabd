package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/agent"
)

// TestReadCreditCompositeKeyStructure verifies that read_file returns a complete
// composite ReadCredit (path, hash, offset, limit, linesRead) and that Registry
// stores it accurately (NBD-034).
func TestReadCreditCompositeKeyStructure(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	path := filepath.Join(dir, "target.txt")
	content := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256([]byte(content))
	wantHash := hex.EncodeToString(h[:])

	raw, _ := json.Marshal(map[string]any{
		"path":   "target.txt",
		"offset": 2,
		"limit":  3,
	})
	out, err := r.RunDetailed(ctx, "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}

	if out.LinesRead != 3 {
		t.Fatalf("out.LinesRead = %d, want 3", out.LinesRead)
	}

	c := out.ReadCredit
	if c.Path != path {
		t.Errorf("ReadCredit.Path = %q, want %q", c.Path, path)
	}
	if c.Hash != wantHash {
		t.Errorf("ReadCredit.Hash = %q, want %q", c.Hash, wantHash)
	}
	if c.Offset != 2 {
		t.Errorf("ReadCredit.Offset = %d, want 2", c.Offset)
	}
	if c.Limit != 3 {
		t.Errorf("ReadCredit.Limit = %d, want 3", c.Limit)
	}
	if c.LinesRead != 3 {
		t.Errorf("ReadCredit.LinesRead = %d, want 3", c.LinesRead)
	}

	// Registry storage and inspection
	r.SetReadCredit(c)
	staged := r.ReadCredit()
	if staged != c {
		t.Errorf("r.ReadCredit() = %+v, want %+v", staged, c)
	}

	// Consuming resets the staged credit
	consumed := r.ConsumeLinesRead()
	if consumed != 3 {
		t.Errorf("r.ConsumeLinesRead() = %d, want 3", consumed)
	}
	if empty := r.ReadCredit(); empty != (agent.ReadCredit{}) {
		t.Errorf("after consume, r.ReadCredit() = %+v, want empty", empty)
	}
}

// TestReadCreditCrossFileRejection verifies that reading file A does NOT grant
// read credit when modifying file B, for both write_file and edit_file (NBD-034 requirement 3).
func TestReadCreditCrossFileRejection(t *testing.T) {
	t.Run("write_file", func(t *testing.T) {
		r, dir := newReg(t)
		ctx := context.Background()

		fileA := filepath.Join(dir, "file_a.txt")
		fileB := filepath.Join(dir, "file_b.txt")
		os.WriteFile(fileA, []byte("content A 1\ncontent A 2\n"), 0o644)
		os.WriteFile(fileB, []byte("content B 1\ncontent B 2\n"), 0o644)

		// 1. Read File A
		rawRead, _ := json.Marshal(map[string]any{"path": "file_a.txt"})
		out, err := r.RunDetailed(ctx, "read_file", rawRead)
		if err != nil || !out.OK {
			t.Fatalf("read file_a: ok=%v err=%v", out.OK, err)
		}
		if out.LinesRead != 2 {
			t.Fatalf("out.LinesRead = %d, want 2", out.LinesRead)
		}
		r.SetReadCredit(out.ReadCredit)

		// 2. Write to File B (cross-file write)
		rawWrite, _ := json.Marshal(map[string]any{
			"path":    "file_b.txt",
			"content": "content B updated\n",
		})
		if _, ok, err := r.Run(ctx, providerToolCall("write_file", rawWrite)); err != nil || !ok {
			t.Fatalf("write_file file_b: ok=%v err=%v", ok, err)
		}

		rec := r.LastEdit()
		if rec == nil {
			t.Fatal("LastEdit() = nil, want an EditRecord")
		}
		if rec.Path != "file_b.txt" {
			t.Errorf("rec.Path = %q, want file_b.txt", rec.Path)
		}
		// Invariant: ReadLines must be 0 because read credit belonged to file_a.txt
		if rec.ReadLines != 0 {
			t.Fatalf("rec.ReadLines = %d, want 0 (cross-file read credit rejected)", rec.ReadLines)
		}

		// Invariant: Staged credit must have been consumed/cleared, so no leak to next write
		if empty := r.ReadCredit(); empty != (agent.ReadCredit{}) {
			t.Errorf("r.ReadCredit() not cleared after cross-file mutation: %+v", empty)
		}
	})

	t.Run("edit_file", func(t *testing.T) {
		r, dir := newReg(t)
		ctx := context.Background()

		fileA := filepath.Join(dir, "file_a.txt")
		fileB := filepath.Join(dir, "file_b.txt")
		os.WriteFile(fileA, []byte("alpha 1\nalpha 2\n"), 0o644)
		os.WriteFile(fileB, []byte("beta original\nbeta line 2\n"), 0o644)

		// 1. Read File A
		rawRead, _ := json.Marshal(map[string]any{"path": "file_a.txt"})
		out, err := r.RunDetailed(ctx, "read_file", rawRead)
		if err != nil || !out.OK {
			t.Fatalf("read file_a: ok=%v err=%v", out.OK, err)
		}
		r.SetReadCredit(out.ReadCredit)

		// 2. Edit File B (cross-file edit)
		rawEdit, _ := json.Marshal(map[string]any{
			"path": "file_b.txt",
			"old":  "beta original",
			"new":  "beta modified",
		})
		if _, ok, err := r.Run(ctx, providerToolCall("edit_file", rawEdit)); err != nil || !ok {
			t.Fatalf("edit_file file_b: ok=%v err=%v", ok, err)
		}

		rec := r.LastEdit()
		if rec == nil {
			t.Fatal("LastEdit() = nil, want an EditRecord")
		}
		if rec.ReadLines != 0 {
			t.Fatalf("rec.ReadLines = %d, want 0 (cross-file read credit rejected for edit_file)", rec.ReadLines)
		}
	})
}

// TestReadCreditExternalModificationInvalidation verifies that if a file is modified
// externally on disk after it was read, any staged read credit is invalidated
// and ReadLines reports 0 (NBD-034 requirement 4).
func TestReadCreditExternalModificationInvalidation(t *testing.T) {
	t.Run("edit_file", func(t *testing.T) {
		r, dir := newReg(t)
		ctx := context.Background()

		filePath := filepath.Join(dir, "doc.txt")
		os.WriteFile(filePath, []byte("heading\nsection 1\nsection 2\n"), 0o644)

		// 1. Read the file
		rawRead, _ := json.Marshal(map[string]any{"path": "doc.txt"})
		out, err := r.RunDetailed(ctx, "read_file", rawRead)
		if err != nil || !out.OK {
			t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
		}
		if out.LinesRead != 3 {
			t.Fatalf("out.LinesRead = %d, want 3", out.LinesRead)
		}
		r.SetReadCredit(out.ReadCredit)

		// 2. External modification on disk before the mutation tool runs
		os.WriteFile(filePath, []byte("heading\nexternally modified 1\nexternally modified 2\n"), 0o644)

		// 3. Edit the file
		rawEdit, _ := json.Marshal(map[string]any{
			"path": "doc.txt",
			"old":  "externally modified 1",
			"new":  "tool edit replacement",
		})
		if _, ok, err := r.Run(ctx, providerToolCall("edit_file", rawEdit)); err != nil || !ok {
			t.Fatalf("edit_file: ok=%v err=%v", ok, err)
		}

		rec := r.LastEdit()
		if rec == nil {
			t.Fatal("LastEdit() = nil, want an EditRecord")
		}
		// Invariant: ReadLines must be 0 because HashBefore does not match the read-time hash
		if rec.ReadLines != 0 {
			t.Fatalf("rec.ReadLines = %d, want 0 (hash mismatch must invalidate read credit)", rec.ReadLines)
		}
	})

	t.Run("write_file", func(t *testing.T) {
		r, dir := newReg(t)
		ctx := context.Background()

		filePath := filepath.Join(dir, "doc.txt")
		os.WriteFile(filePath, []byte("initial line 1\ninitial line 2\n"), 0o644)

		// 1. Read the file
		rawRead, _ := json.Marshal(map[string]any{"path": "doc.txt"})
		out, err := r.RunDetailed(ctx, "read_file", rawRead)
		if err != nil || !out.OK {
			t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
		}
		r.SetReadCredit(out.ReadCredit)

		// 2. External modification
		os.WriteFile(filePath, []byte("third party overwrite\n"), 0o644)

		// 3. Write via write_file
		rawWrite, _ := json.Marshal(map[string]any{
			"path":    "doc.txt",
			"content": "nabd overwrite\n",
		})
		if _, ok, err := r.Run(ctx, providerToolCall("write_file", rawWrite)); err != nil || !ok {
			t.Fatalf("write_file: ok=%v err=%v", ok, err)
		}

		rec := r.LastEdit()
		if rec == nil {
			t.Fatal("LastEdit() = nil, want an EditRecord")
		}
		if rec.ReadLines != 0 {
			t.Fatalf("rec.ReadLines = %d, want 0 (external modification must invalidate credit)", rec.ReadLines)
		}
	})
}

// TestReadCreditValidMatchingCycle verifies that a legitimate read-then-write or
// read-then-edit cycle correctly records ReadLines, and subsequent writes report 0.
func TestReadCreditValidMatchingCycle(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	filePath := filepath.Join(dir, "valid.txt")
	os.WriteFile(filePath, []byte("first\nsecond\nthird\n"), 0o644)

	// 1. Read valid.txt (3 lines)
	rawRead, _ := json.Marshal(map[string]any{"path": "valid.txt"})
	out, err := r.RunDetailed(ctx, "read_file", rawRead)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}
	r.SetReadCredit(out.ReadCredit)

	// 2. Edit valid.txt
	rawEdit, _ := json.Marshal(map[string]any{
		"path": "valid.txt",
		"old":  "second",
		"new":  "SECOND",
	})
	if _, ok, err := r.Run(ctx, providerToolCall("edit_file", rawEdit)); err != nil || !ok {
		t.Fatalf("edit_file: ok=%v err=%v", ok, err)
	}

	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("LastEdit() = nil, want an EditRecord")
	}
	if rec.ReadLines != 3 {
		t.Fatalf("rec.ReadLines = %d, want 3", rec.ReadLines)
	}

	// 3. Second edit without re-reading must report 0
	rawEdit2, _ := json.Marshal(map[string]any{
		"path": "valid.txt",
		"old":  "third",
		"new":  "THIRD",
	})
	if _, ok, err := r.Run(ctx, providerToolCall("edit_file", rawEdit2)); err != nil || !ok {
		t.Fatalf("second edit_file: ok=%v err=%v", ok, err)
	}

	rec2 := r.LastEdit()
	if rec2.ReadLines != 0 {
		t.Fatalf("subsequent edit rec2.ReadLines = %d, want 0", rec2.ReadLines)
	}
}

// TestReadCreditBrandNewFileCreationReportsZero verifies that creating a brand new
// file never carries read credit from an unrelated prior read.
func TestReadCreditBrandNewFileCreationReportsZero(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	fileA := filepath.Join(dir, "existing.txt")
	os.WriteFile(fileA, []byte("some line 1\nsome line 2\n"), 0o644)

	// Read existing file
	rawRead, _ := json.Marshal(map[string]any{"path": "existing.txt"})
	out, err := r.RunDetailed(ctx, "read_file", rawRead)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}
	r.SetReadCredit(out.ReadCredit)

	// Create brand new file
	rawWrite, _ := json.Marshal(map[string]any{
		"path":    "brand_new.txt",
		"content": "brand new content\n",
	})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", rawWrite)); err != nil || !ok {
		t.Fatalf("write_file brand_new: ok=%v err=%v", ok, err)
	}

	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("LastEdit() = nil, want an EditRecord")
	}
	if rec.ReadLines != 0 {
		t.Fatalf("brand new file rec.ReadLines = %d, want 0", rec.ReadLines)
	}
}

// TestReadCreditClearReadState verifies that ClearReadState completely resets
// the staged ReadCredit.
func TestReadCreditClearReadState(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	filePath := filepath.Join(dir, "clear_test.txt")
	os.WriteFile(filePath, []byte("line 1\nline 2\n"), 0o644)

	rawRead, _ := json.Marshal(map[string]any{"path": "clear_test.txt"})
	out, err := r.RunDetailed(ctx, "read_file", rawRead)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}
	r.SetReadCredit(out.ReadCredit)

	if r.ReadCredit() == (agent.ReadCredit{}) {
		t.Fatal("expected staged credit before ClearReadState")
	}

	r.ClearReadState()

	if r.ReadCredit() != (agent.ReadCredit{}) {
		t.Errorf("r.ReadCredit() after ClearReadState = %+v, want empty", r.ReadCredit())
	}

	// A subsequent write must now have ReadLines == 0
	rawWrite, _ := json.Marshal(map[string]any{
		"path":    "clear_test.txt",
		"content": "overwritten\n",
	})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", rawWrite)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}

	rec := r.LastEdit()
	if rec.ReadLines != 0 {
		t.Fatalf("rec.ReadLines = %d after ClearReadState, want 0", rec.ReadLines)
	}
}
