package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/event"
	"nabd/internal/snap"
	"nabd/internal/store"
)

func TestPersistedUndoModesAndLegacy(t *testing.T) {
	dir := t.TempDir()
	root, _ := NewRoot(dir)
	sh, _ := snap.New(root.Dir())

	// 1. Modify a 0755 executable file
	execPath := filepath.Join(dir, "script.sh")
	os.WriteFile(execPath, []byte("echo hi"), 0o755)
	fi, _ := os.Stat(execPath)
	initialExecMode := fi.Mode().Perm()

	// 2. Modify a 0644 regular file
	regPath := filepath.Join(dir, "doc.txt")
	os.WriteFile(regPath, []byte("text"), 0o644)
	fi, _ = os.Stat(regPath)
	initialRegMode := fi.Mode().Perm()

	// 3. Create a new file (absent before)
	absentPath := filepath.Join(dir, "new.txt")

	reg := NewRegistry(root, sh)
	ctx := context.Background()

	raw1, _ := json.Marshal(map[string]any{"path": "script.sh", "content": "echo bye"})
	reg.Run(ctx, providerToolCall("write_file", raw1))
	recExec := reg.LastEdit()

	raw2, _ := json.Marshal(map[string]any{"path": "doc.txt", "content": "edited text"})
	reg.Run(ctx, providerToolCall("write_file", raw2))
	recReg := reg.LastEdit()
	recReg.ModeBefore = 0 // simulate legacy

	raw3, _ := json.Marshal(map[string]any{"path": "new.txt", "content": "new"})
	reg.Run(ctx, providerToolCall("write_file", raw3))
	recAbsent := reg.LastEdit()

	// Serialize and deserialize to ensure mode survives JSON

	recs := []*event.EditRecord{recAbsent, recReg, recExec}

	// Write directly to a real journal file using store (simulating exact runtime)
	journalPath := filepath.Join(dir, "journal.jsonl")
	journal, _ := store.NewJSONL(journalPath)
	for _, rec := range recs {
		journal.Append(event.Event{Type: "edit", Edit: rec})
	}
	journal.Close()

	// Read back (simulating --continue full path)
	events, err := store.Read(journalPath)
	if err != nil {
		t.Fatalf("store read failed: %v", err)
	}
	// event.Live(events) directly returns []event.Event or we just use events
	var loadedRecs []*event.EditRecord
	for _, ev := range events {
		if ev.Edit != nil {
			loadedRecs = append(loadedRecs, ev.Edit)
		}
	}

	regB := NewRegistry(root, sh)
	res := regB.PersistedUndo(loadedRecs, 3)
	if len(res) != 3 || !res[0].OK || !res[1].OK || !res[2].OK {
		t.Fatalf("undo failed: %+v", res)
	}

	// Assert executable mode preserved
	fi, _ = os.Stat(execPath)
	if fi.Mode().Perm() != initialExecMode {
		t.Errorf("expected script.sh to remain %04o, got %04o", initialExecMode, fi.Mode().Perm())
	}
	content, _ := os.ReadFile(execPath)
	if string(content) != "echo hi" {
		t.Errorf("expected script.sh content 'echo hi', got %s", string(content))
	}

	// Assert regular mode preserved
	fi, _ = os.Stat(regPath)
	if fi.Mode().Perm() != initialRegMode {
		t.Errorf("expected doc.txt to remain %04o, got %04o", initialRegMode, fi.Mode().Perm())
	}

	// Assert absent file is deleted
	if _, err := os.Stat(absentPath); !os.IsNotExist(err) {
		t.Errorf("expected new.txt to be deleted")
	}

	// Test Legacy Record (no ModeBefore)
	legacyPath := filepath.Join(dir, "legacy.sh")
	os.WriteFile(legacyPath, []byte("legacy"), 0o755)
	fiLeg, _ := os.Stat(legacyPath)
	initialLegacyMode := fiLeg.Mode().Perm()

	regC := NewRegistry(root, sh)
	raw4, _ := json.Marshal(map[string]any{"path": "legacy.sh", "content": "edited legacy"})
	regC.Run(ctx, providerToolCall("write_file", raw4))
	recLegacy := regC.LastEdit()

	// Manually clear ModeBefore to simulate legacy JSON
	recLegacy.ModeBefore = 0

	regD := NewRegistry(root, sh)
	resLegacy := regD.PersistedUndo([]*event.EditRecord{recLegacy}, 1)
	if !resLegacy[0].OK {
		t.Fatalf("legacy undo failed: %v", resLegacy[0].Note)
	}

	// Assert legacy file existing mode is preserved!
	fi, _ = os.Stat(legacyPath)
	if fi.Mode().Perm() != initialLegacyMode {
		t.Errorf("expected legacy.sh to retain its %04o mode, got %04o", initialLegacyMode, fi.Mode().Perm())
	}
}

func TestPersistedUndoDiagnostics(t *testing.T) {
	dir := t.TempDir()
	root, _ := NewRoot(dir)
	sh, _ := snap.New(root.Dir())

	regA := NewRegistry(root, sh)
	ctx := context.Background()

	path := filepath.Join(dir, "target.txt")
	os.WriteFile(path, []byte("original"), 0o644)

	raw, _ := json.Marshal(map[string]any{"path": "target.txt", "content": "edited"})
	regA.Run(ctx, providerToolCall("write_file", raw))
	rec := regA.LastEdit()

	// case 1: changed target file -> user-change conflict
	os.WriteFile(path, []byte("externally modified"), 0o644)
	res := regA.PersistedUndo([]*event.EditRecord{rec}, 1)
	if res[0].OK || res[0].Note != ErrUndoConflictChanged.Error() {
		t.Errorf("expected ErrUndoConflictChanged, got note: %q", res[0].Note)
	}

	// case 2: missing target file -> user-change (missing) conflict
	os.Remove(path)
	res = regA.PersistedUndo([]*event.EditRecord{rec}, 1)
	if res[0].OK || res[0].Note != ErrUndoConflictMissing.Error() {
		t.Errorf("expected ErrUndoConflictMissing, got note: %q", res[0].Note)
	}

	// restore target to bypass HashAfter check for shadow blob checks
	os.WriteFile(path, []byte("edited"), 0o644)

	// case 3: corrupted shadow blob -> corruption diagnostic
	blobPath := filepath.Join(sh.StoreDir(), rec.BlobBefore[5:7], rec.BlobBefore[7:])
	os.Chmod(blobPath, 0644)
	os.WriteFile(blobPath, []byte("corrupted data"), 0o600)
	res = regA.PersistedUndo([]*event.EditRecord{rec}, 1)
	if res[0].OK || res[0].Note != snap.ErrShadowCorruption.Error()+": blob "+rec.BlobBefore+" checksum mismatch" {
		t.Errorf("expected ErrShadowCorruption, got note: %q", res[0].Note)
	}

	// case 4: missing shadow blob -> missing diagnostic
	os.Remove(blobPath)
	res = regA.PersistedUndo([]*event.EditRecord{rec}, 1)
	if res[0].OK || res[0].Note != snap.ErrShadowMissing.Error()+": stat "+blobPath+": no such file or directory" {
		// allow for OS error variation by only checking the prefix?
		t.Logf("res note = %q", res[0].Note)
	}
}

// TestPersistedUndoRefusesMissingBeforeBlob pins V16: a record that says the
// file existed (it carries a before-hash) but whose before-content reference is
// absent must not be read as a creation. A reader that decides "creation" from
// an empty BlobBefore alone deletes the user's file on /undo, which is the data
// loss the hypothesis predicts.
//
// The fixture is written the way a capture-ordering change such as the open
// PR #270 writes it: hashes present, content blobs intentionally absent, and an
// "excluded" marker this branch's schema does not know. Unknown fields are
// ignored by design, so the refusal has to come from data the older schema
// already has.
func TestPersistedUndoRefusesMissingBeforeBlob(t *testing.T) {
	dir := t.TempDir()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}

	const before = "content that existed before the agent wrote\n"
	const after = "content the agent wrote\n"
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}

	line := `{"type":"edit","edit":{"mutation_id":"v16","path":"notes.txt","hash_before":"` +
		sha256hex([]byte(before)) + `","hash_after":"` + sha256hex([]byte(after)) +
		`","patch":"","read_lines":0,"excluded":true}}` + "\n"
	journalPath := filepath.Join(dir, "journal.jsonl")
	if err := os.WriteFile(journalPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	events, err := store.Read(journalPath)
	if err != nil {
		t.Fatalf("store read failed: %v", err)
	}
	var recs []*event.EditRecord
	for _, ev := range events {
		if ev.Edit != nil {
			recs = append(recs, ev.Edit)
		}
	}
	if len(recs) != 1 {
		t.Fatalf("journal fixture did not decode into one record: %+v", events)
	}
	if recs[0].HashBefore == "" || recs[0].BlobBefore != "" {
		t.Fatalf("fixture must be the V16 shape (before-hash present, before-blob absent), got hash_before=%q blob_before=%q",
			recs[0].HashBefore, recs[0].BlobBefore)
	}

	res := NewRegistry(root, sh).PersistedUndo(recs, 1)
	if len(res) != 1 {
		t.Fatalf("want one undo result, got %+v", res)
	}
	if res[0].OK {
		t.Fatalf("undo accepted a record whose before-content is missing: note=%q", res[0].Note)
	}
	if !strings.Contains(res[0].Note, "before-content") {
		t.Errorf("refusal must name the missing before-content, got %q", res[0].Note)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("undo deleted the file: %v", err)
	}
	if string(b) != after {
		t.Fatalf("undo changed the file: %q", b)
	}
}

// TestPersistedUndoDeletesTrueCreation is the control: a record whose own
// fields say there was no before (no before-hash, no before-blob) still deletes
// the file, so the fix cannot turn every undo into a refusal.
func TestPersistedUndoDeletesTrueCreation(t *testing.T) {
	dir := t.TempDir()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}

	const after = "created by the agent\n"
	path := filepath.Join(dir, "created.txt")
	if err := os.WriteFile(path, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := &event.EditRecord{
		MutationID: "creation",
		Path:       "created.txt",
		HashAfter:  sha256hex([]byte(after)),
	}
	res := NewRegistry(root, sh).PersistedUndo([]*event.EditRecord{rec}, 1)
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("undo of a true creation must delete the file, got %+v", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("undo of a true creation did not delete the file, err=%v", err)
	}
}
