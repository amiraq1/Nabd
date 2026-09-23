package main

import (
	"bytes"
	"strings"
	"testing"

	"nabd/internal/perm"
	"nabd/internal/redact"
)

// TestHeadlessPlainStdoutNotRedactedByDesign pins the design boundary: the plain
// headless answer is the user's requested output, derived from agent history, so
// it is not redacted, while the same credential is redacted in the journal.
func TestHeadlessPlainStdoutNotRedactedByDesign(t *testing.T) {
	setUpProject(t)
	sess := t.TempDir()
	const key = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"

	var stdout, stderr bytes.Buffer
	code := runHeadless(headlessConfig{
		prompt: "go", mode: perm.ModeDeny, sessDir: sess,
		stdout: &stdout, stderr: &stderr,
		provider: &chunkProvider{chunks: []string{"the key is " + key}},
	})
	if code != exitSettled {
		t.Fatalf("exit %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), key) {
		t.Fatalf("plain stdout must carry the requested output verbatim, got %q", stdout.String())
	}
	journal := readOnlyJournal(t, sess)
	if strings.Contains(journal, key) || !strings.Contains(journal, redact.Token) {
		t.Fatalf("journal must redact the key:\n%s", journal)
	}
}
