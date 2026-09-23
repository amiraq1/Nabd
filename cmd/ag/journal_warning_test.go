package main

import (
	"bytes"
	"strings"
	"testing"

	"nabd/internal/store"
)

func TestNewSessionJournalWarnsRedactionIsEnabledByDefault(t *testing.T) {
	isolateJournalConfig(t)
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
	if strings.Count(got, redactedJournalWarning) != 1 {
		t.Fatalf(
			"warning count = %d, want 1; output=%q",
			strings.Count(got, redactedJournalWarning),
			got,
		)
	}
	if !strings.Contains(got, sandboxAuthorityNotice) {
		t.Fatalf("sandbox authority notice missing: %q", got)
	}
	if !strings.Contains(got, "cleartext") {
		t.Fatalf("warning does not identify the remaining cleartext limitation: %q", got)
	}
	if !strings.Contains(got, "0600") {
		t.Fatalf("warning does not explain filesystem mode: %q", got)
	}
	if !strings.Contains(got, "unrecognized") {
		t.Fatalf("warning does not explain the redaction limitation: %q", got)
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

func TestNewSessionJournalWarnsOnExplicitRedactionOptOut(t *testing.T) {
	isolateJournalConfig(t)
	t.Setenv(journalRedactionEnv, "0")

	var warnings bytes.Buffer
	journal, _, err := newSessionJournalWithWarning(t.TempDir(), &warnings)
	if err != nil {
		t.Fatalf("newSessionJournalWithWarning: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := sandboxAuthorityNotice + rawJournalWarning
	if warnings.String() != want {
		t.Fatalf("warning=%q, want %q", warnings.String(), want)
	}
}

func TestNewSessionJournalAllowsNilWarningWriter(t *testing.T) {
	isolateJournalConfig(t)
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

func TestStartupNoticeStatesNoSandbox(t *testing.T) {
	isolateJournalConfig(t)
	var warnings bytes.Buffer
	journal, _, err := newSessionJournalWithWarning(t.TempDir(), &warnings)
	if err != nil {
		t.Fatalf("newSessionJournalWithWarning: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := warnings.String()
	want := "notice: approved bash commands run with the full authority of the Termux app user and there is no filesystem sandbox\n"
	if !strings.Contains(got, want) {
		t.Fatalf("startup notice missing expected text %q; got %q", want, got)
	}
	if strings.Contains(got, "host-dependent") {
		t.Fatalf("startup notice still contains stale 'host-dependent': %q", got)
	}
	if strings.Contains(got, "Landlock") {
		t.Fatalf("startup notice still contains stale 'Landlock': %q", got)
	}
}
