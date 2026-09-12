package main

import (
	"bytes"
	"strings"
	"testing"

	"nabd/internal/store"
)

func TestNewSessionJournalWarnsOutsideJournal(t *testing.T) {
	var warnings bytes.Buffer

	journal, path, err := newSessionJournalWithWarning(
		t.TempDir(),
		&warnings,
	)
	if err != nil {
		t.Fatalf("newSessionJournalWithWarning: %v", err)
	}

	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := warnings.String()
	if strings.Count(got, rawJournalWarning) != 1 {
		t.Fatalf(
			"warning count = %d, want 1; output=%q",
			strings.Count(got, rawJournalWarning),
			got,
		)
	}
	if !strings.Contains(got, "cleartext") {
		t.Fatalf("warning does not identify cleartext storage: %q", got)
	}
	if !strings.Contains(got, "0600") {
		t.Fatalf("warning does not explain filesystem mode: %q", got)
	}
	if !strings.Contains(got, "does not redact") {
		t.Fatalf("warning does not explain redaction limitation: %q", got)
	}

	events, err := store.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf(
			"diagnostic warning leaked into journal: %#v",
			events,
		)
	}
}

func TestNewSessionJournalAllowsNilWarningWriter(t *testing.T) {
	journal, _, err := newSessionJournalWithWarning(
		t.TempDir(),
		nil,
	)
	if err != nil {
		t.Fatalf("newSessionJournalWithWarning: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
