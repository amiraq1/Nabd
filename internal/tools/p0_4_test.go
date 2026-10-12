package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/approval"
	"nabd/internal/event"
)

// shadowContainsMarker reports whether any blob currently persisted in the
// shadow store CONTAINS marker. It is a DISK check, not a struct check: it reads
// the actual files under the store directory. bytes.Contains (not bytes.Equal)
// is used so a marker can be found inside a larger blob, and the callers use
// long distinctive markers so a coincidental match is not credible.
//
// snap.Shadow.store is unexported, so callers pass the store path. Per
// snap.New it is <root>/.ag/shadow.
func shadowContainsMarker(t *testing.T, storePath string, marker []byte) bool {
	t.Helper()
	if storePath == "" {
		t.Fatal("shadow store path is empty; cannot perform disk check")
	}
	found := false
	_ = filepath.Walk(storePath, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		if bytes.Contains(b, marker) {
			found = true
		}
		return nil
	})
	return found
}

// approvedCtx builds a context carrying a one-shot approval token for path,
// bound to the call id providerToolCall uses ("test-id"). It also stamps the
// same call id, so the helper works both through Registry.Run (which re-stamps
// the same id from the provider call) and when commit is called directly.
func approvedCtx(path string) context.Context {
	return approval.WithToken(approval.WithCall(context.Background(), "test-id"), approval.Mint(path, "test-id"))
}

// TestP04ExcludedContentNeverStoredInShadow is the P0-4 (B3 / ADR-5 Option A)
// characterisation test.
//
// The positive control runs FIRST and is asserted before any negative check: a
// non-excluded write MUST be stored, which proves the disk check can detect
// storage at all. Only then is the negative case meaningful.
//
// The excluded write must be refused with ErrExcludedWrite (not merely "some
// error"), neither the OLD nor the NEW excluded content may reach the shadow
// store, and the on-disk file must be left byte-for-byte as it was.
func TestP04ExcludedContentNeverStoredInShadow(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.env\n")
	storePath := filepath.Join(r.root.Dir(), ".ag", "shadow")

	const controlMarker = "CONTROL_VISIBLE_MARKER_2468135790_KEEP\n"
	const oldSecret = "EXCLUDED_OLD_SECRET_MARKER_0123456789_ABCDEF\n"
	const newSecret = "EXCLUDED_NEW_SECRET_MARKER_9876543210_FEDCBA\n"

	// --- Positive control FIRST: a non-excluded write must be stored. ---
	ctrl := filepath.Join(dir, "main.go")
	if err := os.WriteFile(ctrl, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, cerr := commit(context.Background(), r.root, r.sh, &editLog{}, r, "write_file", "main.go", []byte(controlMarker)); cerr != nil {
		t.Fatalf("control commit to main.go failed: %v", cerr)
	}
	if !shadowContainsMarker(t, storePath, []byte(controlMarker)) {
		t.Fatalf("positive control: non-excluded content was NOT stored; the disk check is broken and the negative case below proves nothing")
	}

	// --- Negative: an excluded write with no approval is refused. ---
	secret := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(secret, []byte(oldSecret), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := commit(context.Background(), r.root, r.sh, &editLog{}, r, "write_file", "secret.env", []byte(newSecret))
	if !errors.Is(err, ErrExcludedWrite) {
		t.Fatalf("excluded write must be refused with ErrExcludedWrite, got %v", err)
	}

	// Neither the pre-existing (OLD) nor the attempted (NEW) excluded content
	// may be in the shadow store.
	if shadowContainsMarker(t, storePath, []byte(oldSecret)) {
		t.Errorf("the OLD excluded content was persisted to .ag/shadow — P0-4 vulnerability")
	}
	if shadowContainsMarker(t, storePath, []byte(newSecret)) {
		t.Errorf("the NEW excluded content was persisted to .ag/shadow — P0-4 vulnerability")
	}

	// The file on disk must be unchanged after the refusal.
	if b, rerr := os.ReadFile(secret); rerr != nil || string(b) != oldSecret {
		t.Fatalf("excluded file changed despite refusal: err=%v content=%q", rerr, string(b))
	}
}

// TestP04ApprovedExcludedWriteStoresNothingAndIsNotUndoable covers the approved
// half: with a valid one-shot token the write reaches the disk, but neither the
// old nor the new content is stored in the shadow, and /undo returns
// ErrUndoExcluded without touching the file.
func TestP04ApprovedExcludedWriteStoresNothingAndIsNotUndoable(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.env\n")
	storePath := filepath.Join(r.root.Dir(), ".ag", "shadow")

	const oldSecret = "APPROVED_OLD_SECRET_MARKER_1122334455_AABBCC\n"
	const newSecret = "APPROVED_NEW_SECRET_MARKER_5544332211_CCBBAA\n"
	secret := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(secret, []byte(oldSecret), 0o644); err != nil {
		t.Fatal(err)
	}

	out, ok, err := r.Run(approvedCtx("secret.env"), providerToolCall("write_file", jsonBody("secret.env", newSecret)))
	if !ok || err != nil {
		t.Fatalf("approved write_file failed: ok=%v err=%v out=%q", ok, err, out)
	}
	if b, rerr := os.ReadFile(secret); rerr != nil || string(b) != newSecret {
		t.Fatalf("approved write did not reach disk: err=%v content=%q", rerr, string(b))
	}

	if shadowContainsMarker(t, storePath, []byte(oldSecret)) {
		t.Errorf("approved write stored the OLD excluded content in .ag/shadow")
	}
	if shadowContainsMarker(t, storePath, []byte(newSecret)) {
		t.Errorf("approved write stored the NEW excluded content in .ag/shadow")
	}

	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("expected a LastEdit record for the approved excluded write")
	}
	res := r.PersistedUndo([]*event.EditRecord{rec}, 1)
	if len(res) != 1 || res[0].OK || !errors.Is(res[0].Err, ErrUndoExcluded) {
		t.Fatalf("undo of an excluded write must return ErrUndoExcluded, got %+v", res)
	}
	if b, rerr := os.ReadFile(secret); rerr != nil || string(b) != newSecret {
		t.Fatalf("undo of an excluded write must leave the file as written: err=%v content=%q", rerr, string(b))
	}
}

// legacyRewindRecordV213 replicates the v2.1.3 rewindRecord logic verbatim
// (git show v2.1.3:internal/tools/undo.go). It is the real old reader: it treats
// an empty BlobBefore as "creation → delete the file" and never consults the
// excluded guard. It exists so a NEW-style journal record can be fed to the OLD
// reader to prove the guard keeps the old reader from deleting an existing file.
func legacyRewindRecordV213(r *Registry, rec *event.EditRecord) UndoResult {
	rel, abs, err := writePathFromRoot(r.root, rec.Path)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}
	now, err := captureFromRoot(r.sh, r.root, rel, abs)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}
	if rec.HashAfter != "" {
		if now.Absent {
			return UndoResult{Rel: rec.Path, Note: ErrUndoConflictMissing.Error()}
		}
		nowHash := ""
		if len(now.Blob) > 5 {
			nowHash = now.Blob[5:]
		}
		if nowHash != rec.HashAfter {
			return UndoResult{Rel: rec.Path, Note: ErrUndoConflictChanged.Error()}
		}
	}
	if rec.BlobBefore == "" {
		if err := removeFromRoot(r.root, rel, abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return UndoResult{Rel: rec.Path, Note: err.Error()}
		}
		return UndoResult{Rel: rec.Path, OK: true, Note: "deleted (write_file)"}
	}
	blob, err := r.sh.Read(rec.BlobBefore)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}
	mode := rec.ModeBefore
	if mode == 0 {
		if !now.Absent && now.Mode != 0 {
			mode = now.Mode
		} else {
			mode = 0o644
		}
	}
	if err := writeFromRoot(r.root, rel, abs, blob, mode); err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}
	return UndoResult{Rel: rec.Path, OK: true, Note: "restored (write_file)"}
}

// TestP04LegacyReaderDoesNotDeleteExcludedFile feeds a NEW-style excluded
// record (BlobBefore/BlobAfter = the non-empty guard) to the REAL v2.1.3 reader.
// The old reader treats an empty BlobBefore as "creation → delete", so a record
// that reached it with BlobBefore == "" would delete an existing file. The guard
// is non-empty, so the old reader instead fails on sh.Read(guard) and deletes
// nothing.
func TestP04LegacyReaderDoesNotDeleteExcludedFile(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.env\n")
	secret := filepath.Join(dir, "secret.env")
	const original = "LEGACY_READER_MUST_NOT_DELETE_ME\n"
	if err := os.WriteFile(secret, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// A record exactly as commitExcluded produces it: guard-only blobs, no hash.
	rec := &event.EditRecord{
		MutationID: mutationID(),
		Path:       "secret.env",
		BlobBefore: excludedGuard,
		BlobAfter:  excludedGuard,
		ModeBefore: 0o644,
	}

	res := legacyRewindRecordV213(r, rec)
	if res.OK {
		t.Fatalf("the v2.1.3 reader must not report success for a guard-only record, got %+v", res)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("the v2.1.3 reader deleted an existing excluded file: %v", err)
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != original {
		t.Fatalf("the v2.1.3 reader modified an existing excluded file: err=%v content=%q", err, string(b))
	}
}

// TestP07UndoStopsAtUnloggedExcludedEdit covers the rare /undo state: content
// currently matches the HashAfter of an OLDER recorded edit, but the newest
// record is an excluded (guard-only) edit. /undo walks newest-first and stops at
// the excluded record, so it must NOT proceed to restore the older record over
// content that the excluded edit produced.
func TestP07UndoStopsAtUnloggedExcludedEdit(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.env\n")

	target := filepath.Join(dir, "target.txt")
	const oldContent = "OLDER_RECORDED_CONTENT\n"
	const unloggedContent = "CONTENT_FROM_EXCLUDED_EDIT\n"
	if err := os.WriteFile(target, []byte(unloggedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// An older normal record whose HashAfter equals the current content, with a
	// BlobBefore that would overwrite it if the rewind reached it.
	blob, err := r.sh.CaptureBytes(target, []byte(oldContent), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	oldRec := &event.EditRecord{
		Path:       "target.txt",
		HashBefore: sha256hex([]byte("prior")),
		HashAfter:  sha256hex([]byte(unloggedContent)),
		BlobBefore: blob.Blob,
		ModeBefore: 0o644,
	}
	// The newest record is an excluded edit: the walk must stop here.
	exRec := &event.EditRecord{
		Path:       "secret.env",
		BlobBefore: excludedGuard,
		BlobAfter:  excludedGuard,
	}

	res := r.PersistedUndo([]*event.EditRecord{exRec, oldRec}, 2)
	if len(res) != 1 {
		t.Fatalf("undo must stop at the excluded record and not process the older one, got %d results: %+v", len(res), res)
	}
	if res[0].OK || !errors.Is(res[0].Err, ErrUndoExcluded) {
		t.Fatalf("the first result must be ErrUndoExcluded, got %+v", res[0])
	}
	if b, rerr := os.ReadFile(target); rerr != nil || string(b) != unloggedContent {
		t.Fatalf("an older record overwrote content produced by the excluded edit: err=%v content=%q", rerr, string(b))
	}
}

// jsonBody builds a write_file argument body for the given path and content.
func jsonBody(path, content string) []byte {
	b, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		panic(err)
	}
	return b
}
