package tools

import (
	"errors"
	"os"

	"nabd/internal/agent"
	"nabd/internal/snap"
)

var (
	ErrUndoConflictMissing = errors.New("target file is missing; will not overwrite your deletion")
	ErrUndoConflictChanged = errors.New("changed after the agent wrote it; will not overwrite your work")
)

// UndoResult is one attempted rewind, in the order attempted.
type UndoResult struct {
	Rel  string
	Note string
	OK   bool
}

// drop removes the newest entry. Called only after a restore succeeded, so a
// refused rewind leaves the log intact and the next /undo sees the same head.

// PersistedUndo rewinds edits recorded in the journal (not the in-memory
// log). It is what makes /undo survive a process restart: the records come
// from session history, content comes from the shadow, and the HashAfter
// check refuses to overwrite a file that changed since the agent wrote it.
// recs must be the live branch's edit records, newest first.
func (r *Registry) PersistedUndo(recs []*agent.EditRecord, n int) []UndoResult {
	var out []UndoResult
	for i := 0; i < n && i < len(recs); i++ {
		rec := recs[i]
		if rec == nil {
			continue
		}
		res := r.rewindRecord(rec)
		out = append(out, res)
		if !res.OK {
			break
		}
	}
	return out
}

// rewindRecord restores one persisted record: verify the file still matches
// HashAfter, then put BlobBefore back through the shadow.
func (r *Registry) rewindRecord(rec *agent.EditRecord) UndoResult {
	// D: Restore Only Through a Resolved Absolute Path
	abs, err := r.root.Resolve(rec.Path)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}
	now, err := r.sh.Capture(abs)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}

	// B: Honest Shadow Diagnostics
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
		// Creation: the "before" was absence.
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return UndoResult{Rel: rec.Path, Note: err.Error()}
		}
		return UndoResult{Rel: rec.Path, OK: true, Note: "deleted (write_file)"}
	}

	// Explicitly read the recovery blob to ensure it is available and not corrupt
	if _, err := r.sh.Read(rec.BlobBefore); err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()} // surface the typed shadow error
	}

	// Restore through RestoreAt
	if err := r.sh.RestoreAt(abs, snap.State{Rel: rec.Path, Blob: rec.BlobBefore, Mode: rec.ModeBefore}); err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}
	return UndoResult{Rel: rec.Path, OK: true, Note: "restored (write_file)"}
}

var ErrNoEdits = errors.New("no edits")
