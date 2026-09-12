package tools

import (
	"errors"
	"os"

	"nabd/internal/agent"
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
	// D: Restore Only Through the Descriptor-Relative Path. relative is the
	// filesystem authority; abs is metadata for the messages and the journal.
	rel, abs, err := writePathFromRoot(r.root, rec.Path)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}
	now, err := captureFromRoot(r.sh, r.root, rel, abs)
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
		if err := removeFromRoot(r.root, rel, abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return UndoResult{Rel: rec.Path, Note: err.Error()}
		}
		return UndoResult{Rel: rec.Path, OK: true, Note: "deleted (write_file)"}
	}

	// Read the recovery blob once; Read surfaces the typed shadow error for a
	// missing or corrupt blob.
	blob, err := r.sh.Read(rec.BlobBefore)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}

	// A legacy record carries no ModeBefore. Preserve the mode the file has now
	// (as the path-based restore did), falling back to 0644.
	mode := rec.ModeBefore
	if mode == 0 {
		if !now.Absent && now.Mode != 0 {
			mode = now.Mode
		} else {
			mode = 0o644
		}
	}

	// Restore through the same descriptor-relative write that commit() uses.
	if err := writeFromRoot(r.root, rel, abs, blob, mode); err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error()}
	}
	return UndoResult{Rel: rec.Path, OK: true, Note: "restored (write_file)"}
}

var ErrNoEdits = errors.New("no edits")
