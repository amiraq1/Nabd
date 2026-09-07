package ui

import (
	"strings"
	"testing"

	"nabd/internal/presentation"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestUserCardFullRowBackgroundCoverage tests that user-message cards have the
// dark-gray background (#303030) across their full width, including trailing padding,
// with proper terminal-color fallbacks.
func TestUserCardFullRowBackgroundCoverage(t *testing.T) {
	defer lipgloss.SetColorProfile(termenv.Ascii)

	tests := []struct {
		name    string
		profile termenv.Profile
		wantBg  string
		wantFg  string
	}{
		{
			name:    "TrueColor (#303030 bg, #E0E0E0 fg)",
			profile: termenv.TrueColor,
			wantBg:  "48;2;48;48;48",
			wantFg:  "38;2;224;224;224",
		},
		{
			name:    "ANSI256 (236 bg, 254 fg)",
			profile: termenv.ANSI256,
			wantBg:  "48;5;236",
			wantFg:  "38;5;254",
		},
		{
			name:    "ANSI 4-bit (100m bright black bg, 97m bright white fg)",
			profile: termenv.ANSI,
			wantBg:  "100m",
			wantFg:  "97",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lipgloss.SetColorProfile(tt.profile)
			width := 40
			item := presentation.FeedItem{
				Type: presentation.ItemUserMsg,
				Text: "A short message",
			}
			lines := renderUserMsg(item, width)

			if len(lines) < 2 {
				t.Fatalf("expected at least 2 lines (header + body), got %d", len(lines))
			}

			// Verify header line
			header := lines[0]
			if ansi.StringWidth(header) != width {
				t.Errorf("header width = %d, want %d", ansi.StringWidth(header), width)
			}
			if !strings.Contains(header, tt.wantBg) {
				t.Errorf("header missing expected bg %q: %q", tt.wantBg, header)
			}
			if !strings.Contains(header, "You") {
				t.Errorf("header missing 'You' role label: %q", header)
			}
			if !strings.HasSuffix(header, "\x1b[0m") {
				t.Errorf("header missing trailing ANSI reset: %q", header)
			}

			// Verify body line
			body := lines[1]
			if ansi.StringWidth(body) != width {
				t.Errorf("body width = %d, want %d", ansi.StringWidth(body), width)
			}
			if !strings.Contains(body, tt.wantBg) {
				t.Errorf("body missing expected bg %q: %q", tt.wantBg, body)
			}
			if !strings.Contains(body, tt.wantFg) {
				t.Errorf("body missing expected fg %q: %q", tt.wantFg, body)
			}
			if !strings.Contains(body, "A short message") {
				t.Errorf("body missing original text: %q", body)
			}
			if !strings.HasSuffix(body, "\x1b[0m") {
				t.Errorf("body missing trailing ANSI reset: %q", body)
			}
		})
	}
}

// TestUserCardStyleResetBeforeNextAssistantMessage verifies that styling does not
// leak into subsequent assistant messages or inter-message separators.
func TestUserCardStyleResetBeforeNextAssistantMessage(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	width := 50
	items := []presentation.FeedItem{
		{Type: presentation.ItemUserMsg, Text: "User question"},
		{Type: presentation.ItemAssistant, Text: "Assistant answer"},
	}

	lines := renderItems(items, width)

	// Expected lines:
	// Line 0: "You" (user card header with bg)
	// Line 1: "User question" (user card body with bg)
	// Line 2: "" (separator row)
	// Line 3: "Nabd" (bold green, normal background)
	// Line 4: "Assistant answer" (normal background)
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	// Line 0: user header
	if !strings.Contains(lines[0], "48;2;48;48;48") {
		t.Errorf("line 0 missing user bg: %q", lines[0])
	}
	// Line 1: user body
	if !strings.Contains(lines[1], "48;2;48;48;48") {
		t.Errorf("line 1 missing user bg: %q", lines[1])
	}

	// Line 2: separator row must be outside the gray background
	if lines[2] != "" {
		t.Errorf("separator row must be empty string, got %q", lines[2])
	}

	// Line 3: Nabd label must NOT contain user message background
	if strings.Contains(lines[3], "48;2;48;48;48") || strings.Contains(lines[3], "48;5;236") || strings.Contains(lines[3], "100m") {
		t.Errorf("assistant label leaked user background: %q", lines[3])
	}
	if !strings.Contains(lines[3], "Nabd") {
		t.Errorf("line 3 missing 'Nabd': %q", lines[3])
	}

	// Line 4: Assistant body must NOT contain user message background
	if strings.Contains(lines[4], "48;2;48;48;48") || strings.Contains(lines[4], "48;5;236") || strings.Contains(lines[4], "100m") {
		t.Errorf("assistant body leaked user background: %q", lines[4])
	}
	if !strings.Contains(lines[4], "Assistant answer") {
		t.Errorf("line 4 missing assistant answer: %q", lines[4])
	}
}

// TestUserCardMultilineAndParagraphs verifies multiline user messages and blank rows.
func TestUserCardMultilineAndParagraphs(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	width := 30
	text := "First paragraph\n\nSecond paragraph with multiple words that wrap onto lines"
	item := presentation.FeedItem{
		Type: presentation.ItemUserMsg,
		Text: text,
	}

	lines := renderUserMsg(item, width)

	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}

	for i, l := range lines {
		w := ansi.StringWidth(l)
		if w != width {
			t.Errorf("line %d visual width = %d, want %d", i, w, width)
		}
		if !strings.Contains(l, "48;2;48;48;48") {
			t.Errorf("line %d missing background color: %q", i, l)
		}
		if !strings.HasSuffix(l, "\x1b[0m") {
			t.Errorf("line %d missing reset suffix: %q", i, l)
		}
	}
}

// TestUserCardNarrowWidths verifies that user card lines never exceed terminal width.
func TestUserCardNarrowWidths(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	text := "Testing narrow width wrapping and bounds checking for user cards"
	item := presentation.FeedItem{
		Type: presentation.ItemUserMsg,
		Text: text,
	}

	widths := []int{1, 2, 3, 4, 5, 8, 10, 15, 20}
	for _, w := range widths {
		lines := renderUserMsg(item, w)
		if len(lines) == 0 {
			t.Fatalf("width %d: expected lines, got 0", w)
		}
		for i, l := range lines {
			sw := ansi.StringWidth(l)
			if sw > w {
				t.Errorf("width %d line %d exceeded terminal width: visual width = %d, want <= %d", w, i, sw, w)
			}
			if sw != w {
				t.Errorf("width %d line %d did not fill terminal width: visual width = %d, want %d", w, i, sw, w)
			}
		}
	}
}

// TestUserCardMixedUnicode verifies mixed Arabic, ASCII, emoji, and path rendering.
func TestUserCardMixedUnicode(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	width := 50
	text := "مرحبا 123 😊 /path/to/file.go - test mixed unicode"
	item := presentation.FeedItem{
		Type: presentation.ItemUserMsg,
		Text: text,
	}

	lines := renderUserMsg(item, width)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}

	for i, l := range lines {
		if ansi.StringWidth(l) != width {
			t.Errorf("line %d visual width = %d, want %d", i, ansi.StringWidth(l), width)
		}
	}

	bodyLine := lines[1]
	if !strings.Contains(bodyLine, "مرحبا 123 😊 /path/to/file.go") {
		t.Errorf("body text corrupted or reshaped: %q", bodyLine)
	}
}

// TestUserCardColorDisabled verifies that under NO_COLOR / Ascii profile, no ANSI
// escape codes are emitted, but role labels and width filling remain intact.
func TestUserCardColorDisabled(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)

	width := 40
	item := presentation.FeedItem{
		Type: presentation.ItemUserMsg,
		Text: "Plain text\nSecond line",
	}

	lines := renderUserMsg(item, width)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	for i, l := range lines {
		if strings.Contains(l, "\x1b") {
			t.Errorf("line %d contained ANSI escape sequence under Ascii profile: %q", i, l)
		}
		if ansi.StringWidth(l) != width {
			t.Errorf("line %d visual width = %d, want %d", i, ansi.StringWidth(l), width)
		}
	}

	if !strings.HasPrefix(lines[0], " You") {
		t.Errorf("line 0 missing ' You' prefix: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], " Plain text") {
		t.Errorf("line 1 missing ' Plain text' prefix: %q", lines[1])
	}
}
