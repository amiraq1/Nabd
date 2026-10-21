package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first-byte timeout text used to be duplicated verbatim in anthropic.go
// and openai.go. The provider now carries one ASCII baseline and the CLI
// installs the Arabic catalog through the language seam (ADR-0002 decision 4).
// These tests pin the baseline, the seam, and the single home of the text.
func TestFirstByteTimeoutMessageDefaultsToASCIIBaseline(t *testing.T) {
	got := FirstByteTimeoutMessage()
	if got == "" {
		t.Fatal("first-byte timeout baseline is empty")
	}
	if !testASCIIOnly(got) {
		t.Fatalf("first-byte timeout baseline is not ASCII: %q", got)
	}
}

// An empty install keeps the current value: an install that lost its catalog
// must not blank the message.
func TestFirstByteTimeoutMessageIsInstalledThroughTheSeam(t *testing.T) {
	prev := FirstByteTimeoutMessage()
	t.Cleanup(func() { SetFirstByteTimeoutMessage(prev) })

	SetFirstByteTimeoutMessage("sentinel: first byte never arrived")
	if got := FirstByteTimeoutMessage(); got != "sentinel: first byte never arrived" {
		t.Fatalf("installed message not read back: %q", got)
	}
	SetFirstByteTimeoutMessage("")
	if got := FirstByteTimeoutMessage(); got != "sentinel: first byte never arrived" {
		t.Fatalf("an empty install must keep the current value, got %q", got)
	}
}

// The phrase used to live in two files; the reduction is durable only if a
// second copy cannot creep back in. Non-test provider sources must carry the
// baseline literal in messages.go alone.
func TestFirstByteTimeoutMessageHasOneSourceLiteral(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob provider sources: %v", err)
	}
	var carriers []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(data), defaultFirstByteTimeoutMessage) {
			carriers = append(carriers, file)
		}
	}
	if len(carriers) != 1 || carriers[0] != "messages.go" {
		t.Fatalf("the timeout text must live in exactly one source file (messages.go), found in %v", carriers)
	}
}
