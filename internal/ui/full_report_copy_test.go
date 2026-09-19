package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFullReportCopyRendersAllCardsExpanded verifies that uppercase Y copies
// every card with its tool output expanded, even when the visible viewport has
// tool output collapsed.
func TestFullReportCopyRendersAllCardsExpanded(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"alpha output", "beta output", "gamma output"}, 100)

	// Collapse tool output globally: the visible viewport must not show it,
	// so the report can only contain it by expanding independently.
	m.toolsExpanded = false
	m.refresh()
	for _, l := range m.lines {
		if strings.Contains(l, "alpha output") {
			t.Fatal("precondition: collapsed view unexpectedly shows tool output")
		}
	}

	m.enterNavigation()
	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	if cmd != nil {
		t.Fatal("full report copy returned a non-nil tea.Cmd")
	}
	if buf.Len() == 0 {
		t.Fatal("full report copy wrote no OSC 52 sequence")
	}

	decoded := decodeOSC52Payload(t, buf.String())
	for _, want := range []string{"alpha output", "beta output", "gamma output"} {
		if !strings.Contains(decoded, want) {
			t.Fatalf("full report missing expanded card text %q", want)
		}
	}
	if m.status != copyFullReportNotice {
		t.Fatalf("status = %q, want %q", m.status, copyFullReportNotice)
	}
}

// TestFullReportCopyDoesNotMutateVisibleUI verifies the report is rendered on a
// side path: neither the rendered lines, the offsets, the global expansion
// flag, nor the projector items change.
func TestFullReportCopyDoesNotMutateVisibleUI(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"alpha", "beta"}, 80)
	m.toolsExpanded = false
	m.refresh()
	m.enterNavigation()

	linesBefore := append([]string(nil), m.lines...)
	offsetsBefore := append([]int(nil), m.offsets...)
	expandedBefore := m.toolsExpanded
	itemsBefore := m.proj.Items()
	fps := make([]uint64, len(itemsBefore))
	for i, it := range itemsBefore {
		fps[i] = it.Fingerprint()
	}

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})

	if m.toolsExpanded != expandedBefore {
		t.Fatalf("full report copy changed toolsExpanded: %v -> %v", expandedBefore, m.toolsExpanded)
	}
	if len(m.lines) != len(linesBefore) {
		t.Fatalf("full report copy changed m.lines length: %d -> %d", len(linesBefore), len(m.lines))
	}
	for i := range linesBefore {
		if m.lines[i] != linesBefore[i] {
			t.Fatalf("full report copy mutated m.lines[%d]", i)
		}
	}
	if len(m.offsets) != len(offsetsBefore) {
		t.Fatalf("full report copy changed m.offsets length: %d -> %d", len(offsetsBefore), len(m.offsets))
	}
	for i := range offsetsBefore {
		if m.offsets[i] != offsetsBefore[i] {
			t.Fatalf("full report copy mutated m.offsets[%d]", i)
		}
	}
	itemsAfter := m.proj.Items()
	if len(itemsAfter) != len(itemsBefore) {
		t.Fatalf("full report copy changed projector item count: %d -> %d", len(itemsBefore), len(itemsAfter))
	}
	for i, it := range itemsAfter {
		if it.Fingerprint() != fps[i] {
			t.Fatalf("full report copy mutated projector item %d", i)
		}
	}
}

// TestFullReportCopyRedacts verifies the full report is redacted before it
// reaches the clipboard.
func TestFullReportCopyRedacts(t *testing.T) {
	const secret = "sk-ant-abcdefgh12345678"
	m := feedWithCustomTexts(t, []string{"leaked credential " + secret}, 100)
	m.enterNavigation()

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})

	decoded := decodeOSC52Payload(t, buf.String())
	if strings.Contains(decoded, secret) {
		t.Fatalf("full report leaked raw secret %q", secret)
	}
	if !strings.Contains(decoded, "[REDACTED]") {
		t.Fatalf("full report missing redaction token")
	}
}

// TestOversizedReportWritesPrivateRedactedFile verifies the automatic file
// fallback: an oversized report is written redacted to a private directory
// (0700) with a private file (0600), and never to the clipboard.
func TestOversizedReportWritesPrivateRedactedFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "exports")
	t.Setenv(ExportsDirEnv, dir)

	const secret = "sk-ant-abcdefgh12345678"
	// Each tool card's output is capped at maxToolOutputChars, so many cards
	// are needed to exceed the clipboard ceiling. The filler is non-secret
	// text on its own lines so redaction replaces only the secret, not the
	// whole card (the sk-ant- pattern is greedy over adjacent alphanumerics).
	const cards = 30
	outputs := make([]string, cards)
	for i := range outputs {
		outputs[i] = secret + "\n" + strings.Repeat("filler line for the report export test\n", 100)
	}
	m := feedWithCustomTexts(t, outputs, 200)
	m.enterNavigation()

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	if cmd != nil {
		t.Fatal("oversized export returned a non-nil tea.Cmd")
	}
	if buf.Len() != 0 {
		t.Fatal("oversized report must not write an OSC 52 sequence")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("export directory was not created: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one report file, got %d", len(entries))
	}

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("export dir perm = %o, want 0700", perm)
	}

	path := filepath.Join(dir, entries[0].Name())
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("report file perm = %o, want 0600", perm)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), secret) {
		t.Fatalf("exported report leaked raw secret")
	}
	if !strings.Contains(string(content), "[REDACTED]") {
		t.Fatalf("exported report missing redaction token")
	}
	if !strings.HasPrefix(m.status, copyFullReportSaved) {
		t.Fatalf("status = %q, want prefix %q", m.status, copyFullReportSaved)
	}
}
