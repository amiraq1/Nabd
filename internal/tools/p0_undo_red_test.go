package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/event"
)

// Task 5 (rewindRecord) negative tests.
//
// Compile status (FAILS on the sanctioned base — CORRECTED): an earlier version
// of this header claimed these build against a pre-fix checkout once the
// behaviour-neutral scaffolding is declared. That claim is retracted. The
// package does not compile on the pre-fix tree because p0_4_test.go imports
// nabd/internal/approval, which the fix introduces and the scaffold does not
// provide. See the header of p0_enforcement_test.go for the literal error.
// Run status (NOT VERIFIED): never executed; no Linux runner is reachable from
// this sandbox, and a Windows build is not a valid substitute (safefs falls back
// to a resolve-then-open compatibility path there).
//
// Classification below is derived from reading the pre-fix commit, not from a
// run. Note one trap: a test can fail on the pre-fix code for the WRONG reason.
// TestP05UndoRefusesWhenHashAfterDiffers is exactly that case — the pre-fix
// reader refuses correctly, and it fails only because the scaffolding Err field
// is never populated by pre-fix code. That is a scaffolding artifact, so it is
// labelled a guard, not a RED.

// TestP05UndoRefusesRecordWithoutHashAfter — RED متوقع (غير مثبت: لم يُشغَّل).
//
// Pins that a record with an empty HashAfter is refused rather than treated as
// "nothing to check". The pre-fix reader wraps the whole change check in
// `if rec.HashAfter != ""`, so an empty HashAfter skips verification entirely
// and BlobBefore is restored over whatever is on disk. On the pre-fix code this
// test fails on its first assertion for the intended reason.
func TestP05UndoRefusesRecordWithoutHashAfter(t *testing.T) {
	r, dir := newReg(t)
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blob, err := r.sh.CaptureBytes(target, []byte("previous\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	rec := &event.EditRecord{
		Path:       "t.txt",
		HashBefore: sha256hex([]byte("previous\n")),
		HashAfter:  "", // cannot prove the file is unchanged
		BlobBefore: blob.Blob,
		ModeBefore: 0o644,
	}

	res := r.PersistedUndo([]*event.EditRecord{rec}, 1)
	if len(res) != 1 || res[0].OK {
		t.Fatalf("a record without HashAfter must be refused, got %+v", res)
	}
	if !errors.Is(res[0].Err, ErrUndoConflictChanged) {
		t.Fatalf("refusal must be ErrUndoConflictChanged, got %v", res[0].Err)
	}
	if b, rerr := os.ReadFile(target); rerr != nil || string(b) != "current\n" {
		t.Fatalf("the file must be untouched: err=%v content=%q", rerr, string(b))
	}
}

// TestP05UndoCreationOnAbsentFileReportsAlreadyAbsent — RED متوقع (غير مثبت: لم يُشغَّل).
//
// Pins the creation case: undoing a creation whose file is already gone reports
// "already absent" rather than pretending to have deleted it. The pre-fix reader
// reaches the creation branch, tolerates os.ErrNotExist, and reports
// "deleted (write_file)". On the pre-fix code this test fails on the Note
// assertion for the intended reason.
func TestP05UndoCreationOnAbsentFileReportsAlreadyAbsent(t *testing.T) {
	r, _ := newReg(t)
	// A confirmed creation: no prior content, and no HashAfter.
	rec := &event.EditRecord{Path: "gone.txt"}

	res := r.PersistedUndo([]*event.EditRecord{rec}, 1)
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("undo of an already-absent creation must succeed, got %+v", res)
	}
	if !strings.Contains(res[0].Note, "already absent") {
		t.Fatalf("undo must report 'already absent', got %q", res[0].Note)
	}
}

// TestP05UndoRefusesWhenHashAfterDiffers is a REGRESSION GUARD with a caveat.
// The pre-fix reader already refuses when the file no longer matches HashAfter
// (it compares now.Blob[5:] against rec.HashAfter and returns
// ErrUndoConflictChanged in the Note), so the behaviour it protects is not new.
// It will still fail on the pre-fix code, but only on the errors.Is assertion,
// because pre-fix code never populates the scaffolding Err field. That is a
// scaffolding artifact, not the intended defect — do not count it as a RED. Its
// value is post-fix: it locks the refusal now that Err is populated.
func TestP05UndoRefusesWhenHashAfterDiffers(t *testing.T) {
	r, dir := newReg(t)
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("user-edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blob, err := r.sh.CaptureBytes(target, []byte("before\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	rec := &event.EditRecord{
		Path:       "t.txt",
		HashBefore: sha256hex([]byte("before\n")),
		HashAfter:  sha256hex([]byte("after\n")), // does not match the current content
		BlobBefore: blob.Blob,
		ModeBefore: 0o644,
	}

	res := r.PersistedUndo([]*event.EditRecord{rec}, 1)
	if len(res) != 1 || res[0].OK || !errors.Is(res[0].Err, ErrUndoConflictChanged) {
		t.Fatalf("undo must refuse when the file changed since the write, got %+v", res)
	}
	if b, _ := os.ReadFile(target); string(b) != "user-edit\n" {
		t.Fatalf("the user's content must be preserved, got %q", string(b))
	}
}

// TestP05UndoExcludedRecordIsErrorsIs — RED متوقع (غير مثبت: لم يُشغَّل).
//
// Pins that an excluded record's refusal is returned as ErrUndoExcluded through
// UndoResult.Err, so callers can classify it with errors.Is instead of matching
// a message. The pre-fix reader has no excluded concept: it takes the
// BlobBefore != "" branch, calls sh.Read("excluded:none"), fails on the invalid
// blob id, and returns that generic error with Err left nil. errors.Is therefore
// fails on the pre-fix code for the intended reason.
func TestP05UndoExcludedRecordIsErrorsIs(t *testing.T) {
	r, _ := newReg(t)
	rec := &event.EditRecord{Path: "secret.env", BlobBefore: excludedGuard, BlobAfter: excludedGuard}

	res := r.PersistedUndo([]*event.EditRecord{rec}, 1)
	if len(res) != 1 || res[0].OK {
		t.Fatalf("an excluded record must be refused, got %+v", res)
	}
	if !errors.Is(res[0].Err, ErrUndoExcluded) {
		t.Fatalf("Err must be ErrUndoExcluded for errors.Is, got %v", res[0].Err)
	}
}
