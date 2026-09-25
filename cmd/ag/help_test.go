package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestHelpMentionsUndoAndRewind: /undo and /rewind are typed in the
// interactive prompt, not passed as flags, so --help is the only place a user
// can discover them without starting a session. The descriptions must match
// the README table verbatim — help that disagrees with the documented contract
// is worse than no help.
func TestHelpMentionsUndoAndRewind(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf)
	got := buf.String()

	// The flag list must still be printed (in this test binary it is go test's
	// own set; the real CLI's flags are covered by the built-binary check).
	if !strings.Contains(got, "\n  -") {
		t.Errorf("flag list lost from help output:\n%s", got)
	}

	for _, name := range []string{"/undo", "/rewind"} {
		lines := 0
		for _, ln := range strings.Split(got, "\n") {
			if strings.Contains(ln, name) {
				lines++
			}
		}
		if lines != 1 {
			t.Errorf("help must carry exactly one line for %s, found %d:\n%s", name, lines, got)
		}
	}

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	for _, name := range []string{"/undo", "/rewind"} {
		want := readmeRow(t, string(readme), name)
		if want == "" {
			t.Fatalf("README table row not found for %s", name)
		}
		if !strings.Contains(got, want) {
			t.Errorf("help output is missing the README description for %s (%q):\n%s", name, want, got)
		}
	}
}

// readmeRow extracts the description cell of the README slash-command table
// row for name.
func readmeRow(t *testing.T, readme, name string) string {
	t.Helper()
	for _, ln := range strings.Split(readme, "\n") {
		if !strings.HasPrefix(ln, "| `"+name+"`") {
			continue
		}
		cells := strings.Split(ln, "|")
		if len(cells) < 4 {
			t.Fatalf("README row for %s is malformed: %q", name, ln)
		}
		return strings.TrimSpace(cells[3])
	}
	return ""
}
