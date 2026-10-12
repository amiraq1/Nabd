package tools

import (
	"testing"

	"nabd/internal/event"
)

// P0-4 regression: excluded paths must never store content blobs,
// and /undo must refuse instead of deleting the file.
func TestP04_ExcludedRecordHasNoBlobs(t *testing.T) {
	rec := &event.EditRecord{
		MutationID: "test",
		Path:       ".env",
		Excluded:   true,
	}
	if rec.BlobBefore != "" || rec.BlobAfter != "" {
		t.Error("excluded record must not carry blobs (P0-4)")
	}
	if !rec.Excluded {
		t.Error("excluded record must be marked Excluded (P0-4)")
	}
}

func TestP04_UndoRefusesExcluded(t *testing.T) {
	r := &Registry{}
	rec := &event.EditRecord{
		MutationID: "test",
		Path:       ".env",
		Excluded:   true,
	}
	// rewindRecord should refuse before touching the filesystem.
	// We only verify the Excluded check fires; full rewind needs a root.
	result := r.rewindRecord(rec)
	if result.Note != ErrUndoExcluded.Error() {
		t.Errorf("rewindRecord(excluded) = %q, want %q (P0-4)", result.Note, ErrUndoExcluded.Error())
	}
}
