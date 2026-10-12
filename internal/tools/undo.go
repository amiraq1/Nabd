package tools

import (
	"errors"
	"os"

	"nabd/internal/event"
)

var (
	ErrUndoConflictMissing = errors.New("target file is missing; will not overwrite your deletion")
	ErrUndoConflictChanged = errors.New("changed after the agent wrote it; will not overwrite your work")
	// ErrUndoExcluded is returned by /undo for a record produced by an excluded
	// write (ADR-5 Option A). Excluded content is never captured or stored, so
	// there is no blob to restore; the on-disk file is left exactly as the write
	// left it. Returning this (instead of deleting or restoring) is the safe
	// failure for an excluded path.
	ErrUndoExcluded = errors.New("tools: excluded write cannot be undone")
)

// UndoResult is one attempted rewind, in the order attempted. Err carries the
// typed sentinel (ErrUndoExcluded, ErrUndoConflictMissing, ErrUndoConflictChanged,
// or a shadow/IO error) so callers can classify with errors.Is; Note is the same
// error rendered for display.
type UndoResult struct {
	Rel  string
	Note string
	OK   bool
	Err  error
}

// PersistedUndo rewinds edits recorded in the journal (not the in-memory
// log). It is what makes /undo survive a process restart: the records come
// from session history, content comes from the shadow, and the HashAfter
// check refuses to overwrite a file that changed since the agent wrote it.
// recs must be the live branch's edit records, newest first.
func (r *Registry) PersistedUndo(recs []*event.EditRecord, n int) []UndoResult {
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
func (r *Registry) rewindRecord(rec *event.EditRecord) UndoResult {
	// D: Restore Only Through the Descriptor-Relative Path. relative is the
	// filesystem authority; abs is metadata for the messages and the journal.
	rel, abs, err := writePathFromRoot(r.root, rec.Path)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error(), Err: err}
	}

	// ADR-5 Option A: an excluded write stores no content, so it cannot be
	// undone. The excludedGuard sentinel identifies it. Returning here leaves the
	// on-disk file as written — the safe outcome for an excluded path, and the
	// reason BlobBefore is non-empty (so a legacy reader never reaches its
	// "creation → delete" branch).
	if rec.BlobBefore == excludedGuard || rec.BlobAfter == excludedGuard {
		return UndoResult{Rel: rec.Path, Note: ErrUndoExcluded.Error(), Err: ErrUndoExcluded}
	}

	now, err := captureFromRoot(r.sh, r.root, rel, abs)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error(), Err: err}
	}

	// Change check: the current content must still match HashAfter. The current
	// bytes are read through the shadow and hashed, rather than slicing the blob
	// id, so the check does not depend on the id format.
	//
	// A record with an empty HashAfter cannot prove the file is unchanged, so it
	// is refused — unless it is a confirmed creation (no prior content), which
	// has nothing to conflict with.
	confirmedCreation := rec.HashBefore == "" && rec.BlobBefore == ""
	if rec.HashAfter != "" {
		if now.Absent {
			return UndoResult{Rel: rec.Path, Note: ErrUndoConflictMissing.Error(), Err: ErrUndoConflictMissing}
		}
		cur, rerr := r.sh.Read(now.Blob)
		if rerr != nil {
			return UndoResult{Rel: rec.Path, Note: rerr.Error(), Err: rerr}
		}
		if sha256hex(cur) != rec.HashAfter {
			return UndoResult{Rel: rec.Path, Note: ErrUndoConflictChanged.Error(), Err: ErrUndoConflictChanged}
		}
	} else if !confirmedCreation {
		// No HashAfter and not a confirmed creation: the record cannot prove the
		// file is unchanged, so the rewind is refused rather than overwriting
		// content that was never verified.
		return UndoResult{Rel: rec.Path, Note: ErrUndoConflictChanged.Error(), Err: ErrUndoConflictChanged}
	}

	if rec.BlobBefore == "" {
		// Creation: the "before" was absence.
		if now.Absent {
			return UndoResult{Rel: rec.Path, Note: "already absent", OK: true}
		}
		if err := removeFromRoot(r.root, rel, abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return UndoResult{Rel: rec.Path, Note: err.Error(), Err: err}
		}
		return UndoResult{Rel: rec.Path, OK: true, Note: "deleted (write_file)"}
	}

	// Read the recovery blob once; Read surfaces the typed shadow error for a
	// missing or corrupt blob.
	blob, err := r.sh.Read(rec.BlobBefore)
	if err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error(), Err: err}
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

	// Re-verify immediately before publishing (TOCTOU): the file must still be in
	// the state we captured, or the restore is refused rather than written over
	// content that changed since the check.
	if err := verifyUnchanged(r.sh, r.root, rel, abs, now); err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error(), Err: err}
	}

	// Restore through the same descriptor-relative write that commit() uses.
	if err := writeFromRoot(r.root, rel, abs, blob, mode); err != nil {
		return UndoResult{Rel: rec.Path, Note: err.Error(), Err: err}
	}
	return UndoResult{Rel: rec.Path, OK: true, Note: "restored (write_file)"}
}

var ErrNoEdits = errors.New("no edits")
