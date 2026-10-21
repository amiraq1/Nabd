package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/event"
)

const canaryToken = "NABD_SHADOW_CANARY_2026_91f7c2"

// assertShadowContainsNoCanary recursively walks .ag/shadow and ensures that
// no stored blob file contains the canaryToken bytes.
func assertShadowContainsNoCanary(t *testing.T, shadowDir string) {
	t.Helper()
	if _, err := os.Stat(shadowDir); os.IsNotExist(err) {
		return // Shadow dir doesn't exist yet, clean by definition
	}
	err := filepath.WalkDir(shadowDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(data, []byte(canaryToken)) {
			t.Fatalf("SECURITY VIOLATION (P0-4): canary token %q found on disk in shadow store at %s", canaryToken, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk shadow dir: %v", err)
	}
}

func mustWriteArgs(path, content string) json.RawMessage {
	b, err := json.Marshal(map[string]any{
		"path":    path,
		"content": content,
	})
	if err != nil {
		panic(err)
	}
	return b
}

// TestP04_CanaryExcludedPathNeverStoredInShadow verifies that writing to an excluded
// path (both creation and replacement) NEVER writes the sensitive canary bytes to
// the shadow store on disk (.ag/shadow), while still successfully writing to the target.
func TestP04_CanaryExcludedPathNeverStoredInShadow(t *testing.T) {
	r, _, dir := newGatedReg(t, ".env\nsecrets.env\ndist/\n")
	shadowDir := filepath.Join(dir, ".ag", "shadow")

	// 1. Creation case
	secretCreation := "SECRET_KEY=" + canaryToken + "_CREATION\n"
	out, ok, err := r.Run(context.Background(), providerToolCall("write_file", mustWriteArgs(".env", secretCreation)))
	if !ok || err != nil {
		t.Fatalf("write_file (.env creation) failed: ok=%v err=%v out=%q", ok, err, out)
	}

	// Verify file is on disk
	contentOnDisk, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil || string(contentOnDisk) != secretCreation {
		t.Fatalf("expected .env to be written with canary: %v", err)
	}

	// Check record fields
	lastCreate := r.LastEdit()
	if lastCreate == nil {
		t.Fatal("expected LastEdit record")
	}
	if !lastCreate.Excluded {
		t.Errorf("EditRecord.Excluded must be true for excluded path (P0-4)")
	}
	if lastCreate.BlobBefore != "" || lastCreate.BlobAfter != "" {
		t.Errorf("EditRecord must not have blobs for excluded path: before=%q after=%q", lastCreate.BlobBefore, lastCreate.BlobAfter)
	}
	if lastCreate.Patch != "" {
		t.Errorf("EditRecord.Patch must be empty for excluded path, got %q", lastCreate.Patch)
	}

	// Physical disk verification: NO shadow blob contains canary
	assertShadowContainsNoCanary(t, shadowDir)

	// 2. Replacement case
	secretReplacement := "SECRET_KEY=" + canaryToken + "_REPLACEMENT\n"
	out2, ok2, err2 := r.Run(context.Background(), providerToolCall("write_file", mustWriteArgs(".env", secretReplacement)))
	if !ok2 || err2 != nil {
		t.Fatalf("write_file (.env replacement) failed: ok=%v err=%v out=%q", ok2, err2, out2)
	}

	lastReplace := r.LastEdit()
	if lastReplace == nil {
		t.Fatal("expected LastEdit record for replacement")
	}
	if !lastReplace.Excluded {
		t.Errorf("EditRecord.Excluded must be true for replacement of excluded path")
	}
	if lastReplace.BlobBefore != "" || lastReplace.BlobAfter != "" {
		t.Errorf("EditRecord blobs must be empty for replacement of excluded path")
	}

	// Physical disk verification: still NO shadow blob contains canary
	assertShadowContainsNoCanary(t, shadowDir)
}

// TestP04_CanaryUndoRefusalKeepsFile verifies that /undo refuses on excluded paths
// without deleting or corrupting the user's excluded file.
func TestP04_CanaryUndoRefusalKeepsFile(t *testing.T) {
	r, _, dir := newGatedReg(t, ".env\n")
	envPath := filepath.Join(dir, ".env")
	shadowDir := filepath.Join(dir, ".ag", "shadow")

	// Create .env with canary
	canaryContent := "API_KEY=" + canaryToken + "_VAL\n"
	_, ok, err := r.Run(context.Background(), providerToolCall("write_file", mustWriteArgs(".env", canaryContent)))
	if !ok || err != nil {
		t.Fatalf("write_file failed: %v", err)
	}

	lastEdit := r.LastEdit()
	if lastEdit == nil {
		t.Fatal("expected last edit")
	}

	// Attempt /undo
	res := r.PersistedUndo([]*event.EditRecord{lastEdit}, 1)
	if len(res) != 1 {
		t.Fatalf("expected 1 undo result, got %d", len(res))
	}
	if res[0].OK {
		t.Fatalf("undo on excluded path must NOT succeed: %+v", res[0])
	}
	if res[0].Note != ErrUndoExcluded.Error() {
		t.Fatalf("undo note = %q, want %q", res[0].Note, ErrUndoExcluded.Error())
	}

	// Critical check: file must NOT have been deleted
	kept, rerr := os.ReadFile(envPath)
	if rerr != nil {
		t.Fatalf("excluded file was deleted or cannot be read after refused undo: %v", rerr)
	}
	if string(kept) != canaryContent {
		t.Fatalf("excluded file content was modified by refused undo: got %q, want %q", string(kept), canaryContent)
	}

	// Physical disk verification: the refused undo must not have captured the
	// excluded bytes into shadow before deciding to refuse.
	assertShadowContainsNoCanary(t, shadowDir)
}

// TestP04_CanaryReconcileMutationNeverStoredInShadow verifies that classifying
// an unresolved mutation intent for an excluded path fingerprints the current
// bytes without persisting them in the shadow store.
func TestP04_CanaryReconcileMutationNeverStoredInShadow(t *testing.T) {
	r, _, dir := newGatedReg(t, ".env\n")
	shadowDir := filepath.Join(dir, ".ag", "shadow")
	canaryContent := "API_KEY=" + canaryToken + "_RECONCILE\n"

	_, ok, err := r.Run(context.Background(), providerToolCall("write_file", mustWriteArgs(".env", canaryContent)))
	if !ok || err != nil {
		t.Fatalf("write_file failed: ok=%v err=%v", ok, err)
	}
	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("expected LastEdit record")
	}
	if !rec.Excluded || rec.HashAfter == "" {
		t.Fatalf("record must be excluded and carry HashAfter: %+v", rec)
	}

	state, rerr := r.ReconcileMutation(rec)
	if rerr != nil {
		t.Fatalf("ReconcileMutation: %v", rerr)
	}
	if state != MutationPublished {
		t.Fatalf("reconcile state = %q, want %q", state, MutationPublished)
	}

	// Physical disk verification: reconciliation must not have captured the
	// excluded bytes into shadow while computing the digest.
	assertShadowContainsNoCanary(t, shadowDir)
}

// TestP04_OrdinaryUndoStillWorks verifies that the security fix for excluded files
// does NOT degrade ordinary undo functionality for normal project files.
func TestP04_OrdinaryUndoStillWorks(t *testing.T) {
	r, _, dir := newGatedReg(t, ".env\n")
	docPath := filepath.Join(dir, "ordinary.txt")

	// 1. Create ordinary.txt
	orig := "first line\n"
	_, ok, err := r.Run(context.Background(), providerToolCall("write_file", mustWriteArgs("ordinary.txt", orig)))
	if !ok || err != nil {
		t.Fatalf("create failed: %v", err)
	}
	createRec := r.LastEdit()
	if createRec == nil || createRec.Excluded {
		t.Fatalf("ordinary file must not be excluded: %+v", createRec)
	}
	if createRec.BlobAfter == "" {
		t.Fatal("ordinary file creation must retain BlobAfter")
	}

	// 2. Replace ordinary.txt
	repl := "second line\n"
	_, ok2, err2 := r.Run(context.Background(), providerToolCall("write_file", mustWriteArgs("ordinary.txt", repl)))
	if !ok2 || err2 != nil {
		t.Fatalf("replace failed: %v", err2)
	}
	replaceRec := r.LastEdit()
	if replaceRec == nil || replaceRec.Excluded {
		t.Fatalf("ordinary file replacement must not be excluded")
	}
	if replaceRec.BlobBefore == "" || replaceRec.BlobAfter == "" {
		t.Fatal("ordinary file replacement must retain both blobs")
	}

	// 3. Undo replacement -> restores original content
	res := r.PersistedUndo([]*event.EditRecord{replaceRec}, 1)
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("undo replacement failed: %+v", res)
	}
	restored, err := os.ReadFile(docPath)
	if err != nil || string(restored) != orig {
		t.Fatalf("undo failed to restore ordinary file: got %q, want %q", string(restored), orig)
	}

	// 4. Undo creation -> deletes the newly created file
	res2 := r.PersistedUndo([]*event.EditRecord{createRec}, 1)
	if len(res2) != 1 || !res2[0].OK {
		t.Fatalf("undo creation failed: %+v", res2)
	}
	if _, err := os.Stat(docPath); !os.IsNotExist(err) {
		t.Fatalf("undo creation must delete file, stat err: %v", err)
	}
}
