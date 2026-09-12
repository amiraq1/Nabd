package main

import (
	"io"

	"nabd/internal/store"
)

const (
	rawJournalWarning = "warning: session journal stores cleartext working data; mode 0600 limits filesystem access but does not redact journal contents\n"

	redactedJournalWarning = "warning: journal credential redaction is enabled; unrecognized sensitive content remains cleartext, and mode 0600 only limits filesystem access\n"
)

// newSessionJournalWithWarning creates a new journal using the process-level
// policy and writes a diagnostic directly to the supplied stream. The warning
// bypasses agent sinks and therefore never becomes a journal event.
//
// A warning-write failure does not invalidate an otherwise usable journal.
func newSessionJournalWithWarning(dir string, warnings io.Writer) (*store.JSONL, string, error) {
	opts := journalStoreOptions()

	journal, path, err := newSessionJournalWithOptions(dir, opts)
	if err != nil {
		return nil, "", err
	}

	warning := rawJournalWarning
	if opts.Redact != nil {
		warning = redactedJournalWarning
	}
	if warnings != nil {
		_, _ = io.WriteString(warnings, warning)
	}

	return journal, path, nil
}
