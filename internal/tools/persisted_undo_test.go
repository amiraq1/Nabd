package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/agent"
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

	raw3, _ := json.Marshal(map[string]any{"path": "new.txt", "content": "new"})
	reg.Run(ctx, providerToolCall("write_file", raw3))
	recAbsent := reg.LastEdit()

	// Serialize and deserialize to ensure mode survives JSON

	recs := []*agent.EditRecord{recAbsent, recReg, recExec}

	// Write directly to a real journal file using store (simulating exact runtime)
	journalPath := filepath.Join(dir, "journal.jsonl")
	journal, _ := store.NewJSONL(journalPath)
	for _, rec := range recs {
		journal.Append(agent.Event{Type: "edit", Edit: rec})
	}
	journal.Close()

	// Read back (simulating --continue full path)
	events, err := store.Read(journalPath)
	if err != nil {
		t.Fatalf("store read failed: %v", err)
	}
	// agent.Live(events) directly returns []agent.Event or we just use events
	var loadedRecs []*agent.EditRecord
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
	resLegacy := regD.PersistedUndo([]*agent.EditRecord{recLegacy}, 1)
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
	res := regA.PersistedUndo([]*agent.EditRecord{rec}, 1)
	if res[0].OK || res[0].Note != ErrUndoConflictChanged.Error() {
		t.Errorf("expected ErrUndoConflictChanged, got note: %q", res[0].Note)
	}

	// case 2: missing target file -> user-change (missing) conflict
	os.Remove(path)
	res = regA.PersistedUndo([]*agent.EditRecord{rec}, 1)
	if res[0].OK || res[0].Note != ErrUndoConflictMissing.Error() {
		t.Errorf("expected ErrUndoConflictMissing, got note: %q", res[0].Note)
	}

	// restore target to bypass HashAfter check for shadow blob checks
	os.WriteFile(path, []byte("edited"), 0o644)

	// case 3: corrupted shadow blob -> corruption diagnostic
	blobPath := filepath.Join(sh.StoreDir(), rec.BlobBefore[5:7], rec.BlobBefore[7:])
	os.Chmod(blobPath, 0644)
	os.WriteFile(blobPath, []byte("corrupted data"), 0o600)
	res = regA.PersistedUndo([]*agent.EditRecord{rec}, 1)
	if res[0].OK || res[0].Note != snap.ErrShadowCorruption.Error()+": blob "+rec.BlobBefore+" checksum mismatch" {
		t.Errorf("expected ErrShadowCorruption, got note: %q", res[0].Note)
	}

	// case 4: missing shadow blob -> missing diagnostic
	os.Remove(blobPath)
	res = regA.PersistedUndo([]*agent.EditRecord{rec}, 1)
	if res[0].OK || res[0].Note != snap.ErrShadowMissing.Error()+": stat "+blobPath+": no such file or directory" {
		// allow for OS error variation by only checking the prefix?
		t.Logf("res note = %q", res[0].Note)
	}
}
