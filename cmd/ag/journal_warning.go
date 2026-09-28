package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"nabd/internal/store"
)

const (
	rawJournalWarning = "warning: journal credential redaction is disabled by explicit opt-out; session files may contain sensitive cleartext data and mode 0600 only limits filesystem access\n"

	redactedJournalWarning = "warning: journal credential redaction is enabled by default; unrecognized sensitive content remains cleartext, and mode 0600 only limits filesystem access\n"

	// sessionDirSizeWarnBytes triggers a storage warning when a new session
	// starts: journals are append-only per session, so without rotation or
	// purge the directory grows without bound (M2). Oversized single
	// journals are archived+rotated on --continue; this covers the
	// cross-session accumulation that rotation cannot see.
	sessionDirSizeWarnBytes = 256 << 20
)

// newSessionJournalWithWarning creates a new journal using the process-level
// policy and writes a diagnostic directly to the supplied stream. The warning
// bypasses agent sinks and therefore never becomes a journal event.
//
// A warning-write failure does not invalidate an otherwise usable journal.
func newSessionJournalWithWarning(dir string, warnings io.Writer, exactKeys []string) (*store.JSONL, string, error) {
	opts := journalStoreOptions(exactKeys)

	journal, path, err := newSessionJournalWithOptions(dir, opts)
	if err != nil {
		return nil, "", err
	}

	writeSessionPolicyWarnings(warnings, opts.Redact != nil)
	warnIfSessionDirOversized(dir, warnings)

	return journal, path, nil
}

// warnIfSessionDirOversized warns once per new session when accumulated
// journals exceed sessionDirSizeWarnBytes, pointing at `purge` for cleanup.
// Best-effort: a stat failure never blocks session creation.
func warnIfSessionDirOversized(dir string, w io.Writer) {
	if w == nil {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var total int64
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	if total > sessionDirSizeWarnBytes {
		fmt.Fprintf(w, "warning: session directory holds %d MB of journals; run `nabd purge --dir %s` to reclaim space\n",
			total>>20, dir)
	}
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
