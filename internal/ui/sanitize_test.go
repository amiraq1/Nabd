package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

func TestAssistantTextCannotInjectTerminalControls(t *testing.T) {
	// ESC[2J clears screen, ESC[H moves cursor to home
	malicious := "Hello \x1b[2J\x1b[Hworld! \x1b[31mRed text\x1b[0m"
	f := newFeedAt(t, 80, 24)
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.TextDelta, Text: malicious},
	}})
	v := f.View()
	if strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x1b[H") {
		t.Fatalf("assistant output leaked raw terminal control escapes: %q", v)
	}
}

func TestToolOutputCannotEmitOSC52Clipboard(t *testing.T) {
	// OSC 52 attempts to hijack the system clipboard
	osc52 := "\x1b]52;c;Y2F0Cg==\x07echoed"
	lines := truncateOutput(osc52, 80)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "\x1b]52;") || strings.Contains(joined, "\x07") {
		t.Fatalf("tool output leaked OSC 52 clipboard escape: %q", joined)
	}
}

func TestToolOutputCannotEmitOSC8Hyperlink(t *testing.T) {
	// OSC 8 attempts to inject terminal hyperlink
	osc8 := "\x1b]8;;https://evil.com\x1b\\click here\x1b]8;;\x1b\\"
	lines := truncateOutput(osc8, 80)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "\x1b]8;;") || strings.Contains(joined, "evil.com") {
		t.Fatalf("tool output leaked OSC 8 hyperlink escape: %q", joined)
	}
}

func TestUserInputCannotMoveTerminalCursor(t *testing.T) {
	// User message with cursor movement
	maliciousUser := "user input \x1b[10;20Hjump cursor"
	it := presentation.FeedItem{
		Type: presentation.ItemUserMsg,
		Text: maliciousUser,
	}
	rendered := renderUserMsg(it, 80)
	joined := strings.Join(rendered, "\n")
	if strings.Contains(joined, "\x1b[10;20H") {
		t.Fatalf("renderUserMsg leaked cursor repositioning escape: %q", joined)
	}
}

func TestNoticeCannotSetTerminalTitle(t *testing.T) {
	// OSC 0 / OSC 2 sets window title
	titleHack := "notice text \x1b]0;Pwned Title\x07done"
	it := presentation.FeedItem{
		Type: presentation.ItemNotice,
		Text: titleHack,
	}
	rendered := renderNotice(it, 80)
	joined := strings.Join(rendered, "\n")
	if strings.Contains(joined, "\x1b]0;") || strings.Contains(joined, "Pwned Title") {
		t.Fatalf("renderNotice leaked title change escape: %q", joined)
	}
}

func TestPermissionArgsAreRedactedBeforeTruncation(t *testing.T) {
	// Secret key pattern must be redacted before rune truncation
	secret := "Bearer ghp_abcdefghijklmnopqrstuvwxyz1234567890 extra long parameters"
	res := safeArgs(secret, 20)
	if strings.Contains(res, "ghp_") {
		t.Fatalf("safeArgs leaked secret prefix before truncation: %q", res)
	}
	if !strings.Contains(res, "[REDACTED]") {
		t.Fatalf("safeArgs did not redact secret: %q", res)
	}
}

func TestSanitizerNeutralizesBidiOverrideInToolArgs(t *testing.T) {
	// RLO (U+202E) spoofing filename
	spoofed := "my_normal_doc\u202Etxt.exe"
	sanitized := SanitizeForDisplay(spoofed, DisplayPolicy{AllowNewline: false, Redact: false})
	if strings.Contains(sanitized, "\u202E") {
		t.Fatalf("sanitizer did not strip bidi override U+202E: %q", sanitized)
	}
}

func TestSanitizerPreservesArabicCombiningMarks(t *testing.T) {
	arabic := "اَلْعَرَبِيَّةُ"
	sanitized := SanitizeForDisplay(arabic, DisplayPolicy{AllowNewline: false, Redact: false})
	if sanitized != arabic {
		t.Fatalf("sanitizer altered Arabic text with combining marks: got %q, want %q", sanitized, arabic)
	}
}

func TestSanitizerPreservesZWJEmojiSequences(t *testing.T) {
	emojiFamily := "👨‍👩‍👧‍👦" // Contains ZWJ (U+200D) between family members
	sanitized := SanitizeForDisplay(emojiFamily, DisplayPolicy{AllowNewline: false, Redact: false})
	if sanitized != emojiFamily {
		t.Fatalf("sanitizer destroyed ZWJ emoji sequence: got %q, want %q", sanitized, emojiFamily)
	}
}

func TestSanitizerOutputIsAlwaysValidUTF8(t *testing.T) {
	invalidUTF8 := string([]byte{'f', 'o', 'o', 0xff, 0xfe, 'b', 'a', 'r'})
	sanitized := SanitizeForDisplay(invalidUTF8, DisplayPolicy{AllowNewline: false, Redact: false})
	if !utf8.ValidString(sanitized) {
		t.Fatalf("sanitizer output is not valid UTF-8: %q", sanitized)
	}
	if !strings.Contains(sanitized, "\uFFFD") {
		t.Fatalf("sanitizer did not replace invalid byte with U+FFFD: %q", sanitized)
	}
}

func TestSanitizerPreservesApplicationStyling(t *testing.T) {
	// Application styles (e.g. good.Render, bad.Render) are applied AFTER sanitization,
	// so the final line retains lipgloss formatting.
	rawText := "status alert \x1b[31mspoofed color\x1b[0m"
	it := presentation.FeedItem{
		Type: presentation.ItemNotice,
		Text: rawText,
	}
	rendered := renderNotice(it, 80)
	joined := strings.Join(rendered, "\n")
	if strings.Contains(joined, "\x1b[31m") {
		t.Fatalf("untrusted ANSI color leaked into rendered line: %q", joined)
	}
	// But the application style badge (warn color) MUST still be present
	if !strings.Contains(joined, "⚑") {
		t.Fatalf("application notice symbol missing: %q", joined)
	}
}

func TestSanitizedTextWidthMatchesRenderedCells(t *testing.T) {
	textWithControls := "normal\x1b[5m text\x1b[0m"
	sanitized := SanitizeForDisplay(textWithControls, DisplayPolicy{AllowNewline: false, Redact: false})
	width := ansi.StringWidth(sanitized)
	if width != len("normal text") {
		t.Fatalf("sanitized width %d != expected cell width %d (%q)", width, len("normal text"), sanitized)
	}
}

func TestJournalBytesRemainUnsanitized(t *testing.T) {
	// Invariant: journal event bytes are untouched by display sanitization
	rawPayload := "original \x1b[31muntouched\x1b[0m bytes"
	p := presentation.NewProjector()
	_ = p.Apply(agent.Event{
		Seq:  1,
		Type: agent.UserMsg,
		Text: rawPayload,
	})
	items := p.Items()
	if len(items) == 0 || items[0].Text != rawPayload {
		t.Fatalf("projector mutated journal text: got %q, want %q", items[0].Text, rawPayload)
	}
}
