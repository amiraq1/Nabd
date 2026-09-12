package main

import (
	"io"

	"nabd/internal/store"
)

const rawJournalWarning = "warning: session journal stores cleartext working data; mode 0600 limits filesystem access but does not redact journal contents\n"

// newSessionJournalWithWarning creates a new journal and writes a warning
// directly to the caller's diagnostic stream. The warning deliberately bypasses
// agent sinks, so it never becomes part of the journal itself.
//
// A warning-write failure does not invalidate an otherwise usable journal.
func newSessionJournalWithWarning(dir string, warnings io.Writer) (*store.JSONL, string, error) {
	journal, path, err := newSessionJournal(dir)
	if err != nil {
		return nil, "", err
	}
	if warnings != nil {
		_, _ = io.WriteString(warnings, rawJournalWarning)
	}
	return journal, path, nil
}
