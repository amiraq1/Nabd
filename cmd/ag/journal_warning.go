package main

import (
	"io"

	"nabd/internal/store"
)

const (
	rawJournalWarning = "warning: journal credential redaction is disabled by explicit opt-out; session files may contain sensitive cleartext data and mode 0600 only limits filesystem access\n"

	redactedJournalWarning = "warning: journal credential redaction is enabled by default; unrecognized sensitive content remains cleartext, and mode 0600 only limits filesystem access\n"
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

	writeSessionPolicyWarnings(warnings, opts.Redact != nil)

	return journal, path, nil
}

func writeSessionPolicyWarnings(w io.Writer, redacted bool) {
	writeSandboxAuthorityNotice(w)
	if w == nil {
		return
	}
	warning := rawJournalWarning
	if redacted {
		warning = redactedJournalWarning
	}
	_, _ = io.WriteString(w, warning)
}
