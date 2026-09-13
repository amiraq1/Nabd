package ui

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStripCardGutterPreservesSingleSpaceArabic(t *testing.T) {
	const indentedArabic = " مرحبا بالعالم"
	if got := stripCardGutter(indentedArabic); got != indentedArabic {
		t.Fatalf("stripCardGutter(%q) = %q, want unchanged", indentedArabic, got)
	}
	if !utf8.ValidString(stripCardGutter(indentedArabic)) {
		t.Fatal("stripCardGutter produced invalid UTF-8")
	}
	if got := stripCardGutter("> "+indentedArabic); got != indentedArabic {
		t.Fatalf("selected gutter: got %q, want %q", got, indentedArabic)
	}
	if got := stripCardGutter("  "+indentedArabic); got != indentedArabic {
		t.Fatalf("unselected gutter: got %q, want %q", got, indentedArabic)
	}
}

func TestSearchDoesNotCorruptIndentedArabic(t *testing.T) {
	const text = " مرحبا بالعالم"
	m := feedWithCustomTexts(t, []string{text}, 80)
	m.enterNavigation()

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("مرحبا")})

	if len(m.search.matches) == 0 {
		t.Fatal("search failed to find indented Arabic text")
	}
	for _, line := range m.lines {
		if !utf8.ValidString(line) {
			t.Fatalf("search line contains invalid UTF-8: %q", line)
		}
	}
}

func TestCopyDoesNotCorruptIndentedArabic(t *testing.T) {
	const text = " مرحبا بالعالم"
	m := feedWithCustomTexts(t, []string{text}, 80)
	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Fatal("copy returned a non-nil tea.Cmd")
	}

	decoded := decodeOSC52Payload(t, buf.String())
	if !utf8.ValidString(decoded) {
		t.Fatalf("clipboard payload contains invalid UTF-8: %q", decoded)
	}
	if !strings.Contains(decoded, text) {
		t.Fatalf("clipboard payload = %q, want it to contain %q", decoded, text)
	}
}

func TestOversizedCopyUsesTooLargeNotice(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"small"}, 80)
	m.enterNavigation()
	m.selectItem(0)
	m.lines = []string{"> " + strings.Repeat("A", defaultMaxCopyBytes+1)}
	m.offsets = []int{0}

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Fatal("oversized copy returned a non-nil tea.Cmd")
	}
	if buf.Len() != 0 {
		t.Fatal("oversized copy wrote an OSC 52 sequence")
	}
	if m.status != copyTooLargeNotice {
		t.Fatalf("status = %q, want %q", m.status, copyTooLargeNotice)
	}
	if strings.Contains(strings.ToLower(m.status), "sensitive") {
		t.Fatalf("oversized copy was mislabeled as sensitive content: %q", m.status)
	}
}
