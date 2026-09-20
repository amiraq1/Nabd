package ui

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"nabd/internal/agent"
)

func TestStripCardGutterPreservesSingleSpaceArabic(t *testing.T) {
	const indentedArabic = " مرحبا بالعالم"
	if got := stripCardGutter(indentedArabic); got != indentedArabic {
		t.Fatalf("stripCardGutter(%q) = %q, want unchanged", indentedArabic, got)
	}
	if !utf8.ValidString(stripCardGutter(indentedArabic)) {
		t.Fatal("stripCardGutter produced invalid UTF-8")
	}
	if got := stripCardGutter("> " + indentedArabic); got != indentedArabic {
		t.Fatalf("selected gutter: got %q, want %q", got, indentedArabic)
	}
	if got := stripCardGutter("  " + indentedArabic); got != indentedArabic {
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

// Oversized content is now built at the item level instead of being injected
// into m.lines, because copy reads the projection, not the rendered viewport.
// The guarantee under test is unchanged: no OSC 52 write, no tea.Cmd, and the
// too-large notice rather than a sensitive-content label.
func TestOversizedCopyUsesTooLargeNotice(t *testing.T) {
	huge := strings.Repeat("A", defaultMaxCopyBytes+1)
	m := NewFeed()
	m.width = 80
	m.height = 20
	m.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: huge},
	})
	m.enterNavigation()
	m.selectItem(0)

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

// Regression lock for the wrap bug: a path longer than the viewport must
// survive the copy pipeline as one unbroken line.
func TestCopyKeepsLongPathUnwrapped(t *testing.T) {
	long := "/data/data/com.termux/files/home/" + strings.Repeat("segment/", 20) + "file.go"
	m := feedWithCustomTexts(t, []string{long}, 40)
	m.enterNavigation()
	m.selectItem(0)

	got := m.cardTextForCopyUnwrapped(0)
	if !strings.Contains(got, long) {
		t.Fatalf("long path was wrapped by the copy path: %q", got)
	}
}

// Copy must honour the per-card expansion override, not the global flag:
// renderItemsCached ignores its variadic argument and calls expansionOf per
// card, so the copy path has to do the same or it copies a view the user is
// not looking at.
func TestCopyHonoursPerCardExpansion(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"alpha"}, 40)
	m.enterNavigation()
	m.selectItem(0)
	m.toolsExpanded = false

	collapsed := m.cardTextForCopyUnwrapped(0)
	if !m.toggleCard(0) {
		t.Skip("selected card is not expandable in this fixture")
	}
	expanded := m.cardTextForCopyUnwrapped(0)
	if expanded == collapsed {
		t.Fatal("copy ignored the per-card expansion override")
	}
}

// End-to-end lock for the wrap bug at the real device width: the marker must
// survive projection, rendering, redaction, sanitization, base64 and the OSC 52
// frame as one unbroken token. Unit coverage of cardTextForCopyUnwrapped alone
// cannot catch a regression introduced later in the pipeline.
func TestCopyEndToEndKeepsMarkerAtWidth63(t *testing.T) {
	mark := "ZQ7X/" + strings.Repeat("seg/", 18) + "end.go"
	m := feedWithCustomTexts(t, []string{mark}, 63)
	m.enterNavigation()
	m.selectItem(0)
	m.toolsExpanded = true

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	raw := buf.String()
	if raw == "" {
		t.Fatal("no OSC 52 sequence was written")
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(raw, "\x1b]52;c;"), "\x1b\\")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("clipboard payload is not valid base64: %v", err)
	}
	if !strings.Contains(string(decoded), mark) {
		t.Fatalf("marker was broken by the copy pipeline at width 63:\n%q", string(decoded))
	}
}

// The device path is not the OSC 52 path: on Termux, dispatchCopy hands the
// payload to termux-clipboard-set over stdin. The unwrap fix lives in the
// shared source, but only this test proves the marker survives the transport
// the device actually uses.
func TestCopyTermuxPathKeepsMarkerAtWidth63(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "captured.txt")
	script := filepath.Join(dir, "fake-clip")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > \""+out+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERMUX_VERSION", "0.119.0")
	t.Setenv("SSH_CONNECTION", "")

	mark := "ZQ7X/" + strings.Repeat("seg/", 18) + "end.go"
	m := feedWithCustomTexts(t, []string{mark}, 63)
	m.enterNavigation()
	m.selectItem(0)
	m.toolsExpanded = true
	m.clipboardWriter = nil
	m.clipboardCommand = script

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("termux path returned no command")
	}
	cmd()

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("clipboard command received nothing: %v", err)
	}
	if !strings.Contains(string(got), mark) {
		t.Fatalf("marker broken on the termux transport:\n%q", string(got))
	}
}

// Default Termux transport is OSC 52: no subprocess, no termux-api dependency.
func TestTermuxTransportPrefersOSC52(t *testing.T) {
	t.Setenv("NABD_CLIPBOARD", "")
	m := feedWithCustomTexts(t, []string{"x"}, 40)
	var buf bytes.Buffer
	m.clipboardWriter = &buf
	m.clipboardCommand = ""

	cmd := m.termuxClipboardCmd(defaultClipboardCommand, redactForExport("hello-osc"), copySuccessNotice)
	res, ok := cmd().(clipboardResultMsg)
	if !ok {
		t.Fatal("transport did not return clipboardResultMsg")
	}
	if res.err != nil {
		t.Fatalf("osc transport failed: %v", res.err)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(buf.String(), "\x1b]52;c;"), "\x1b\\")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || !strings.Contains(string(decoded), "hello-osc") {
		t.Fatalf("osc payload wrong: %q (err %v)", buf.String(), err)
	}
}

// NABD_CLIPBOARD=exec forces the verifiable subprocess transport and must not
// emit any escape sequence.
func TestTermuxTransportHonoursExecOverride(t *testing.T) {
	t.Setenv("NABD_CLIPBOARD", "exec")
	m := feedWithCustomTexts(t, []string{"x"}, 40)
	var buf bytes.Buffer
	m.clipboardWriter = &buf
	m.clipboardCommand = ""

	cmd := m.termuxClipboardCmd("cat", redactForExport("hello-exec"), copySuccessNotice)
	if _, ok := cmd().(clipboardResultMsg); !ok {
		t.Fatal("exec transport did not return clipboardResultMsg")
	}
	if buf.Len() != 0 {
		t.Fatalf("exec override still wrote an escape sequence: %q", buf.String())
	}
}
