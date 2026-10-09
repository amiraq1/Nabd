package ui

import (
	"strings"
	"testing"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

// NABD_ASCII_ONLY must replace decorative feed glyphs with ASCII equivalents
// while preserving content text (including Arabic and Unicode punctuation
// inside user/model text). See docs/CONFIG.md.
func TestFeedRenderASCIIOnlyReplacesChrome(t *testing.T) {
	t.Setenv("NABD_ASCII_ONLY", "1")

	arabic := "مرحبا بالعالم هذا نص عربي يجب أن يبقى كما هو"
	// Unicode punctuation inside *content* must survive ASCII mode untouched:
	// the mapping applies to chrome literals emitted by the renderer, never
	// to text passed through SanitizeForDisplay.
	tricky := "نص فيه … و· و✓ و✗ و⚑ و── يجب أن يبقى كما هو"
	longErr := strings.Repeat("e", 200)
	width := 80
	items := []presentation.FeedItem{
		{Type: presentation.ItemAssistant, Text: ""}, // empty -> bullet
		{Type: presentation.ItemAssistant, Text: arabic},
		{Type: presentation.ItemAssistant, Text: tricky},
		{Type: presentation.ItemError, Text: longErr}, // truncation tail
		{Type: presentation.ItemNotice, Text: "take note"},
		{Type: presentation.ItemRunBoundary, Text: "step 1"},
		{Type: presentation.ItemPermission, Perm: &presentation.PermCard{Status: presentation.PermAllow, Name: "bash"}},
		{Type: presentation.ItemPermission, Perm: &presentation.PermCard{Status: presentation.PermDeny, Name: "bash"}},
	}

	var sb strings.Builder
	for _, line := range renderItems(items, width) {
		// The ASCII tail is wider than "…": widths must be computed with
		// the replacement applied, or lines overflow narrow screens.
		if got := ansi.StringWidth(line); got > width {
			t.Errorf("line exceeds width %d: %d cells: %q", width, got, line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	out := sb.String()

	for _, content := range []string{arabic, tricky} {
		if !strings.Contains(out, content) {
			t.Errorf("ASCII mode must preserve content text %q", content)
		}
	}

	// Chrome checks run on items carrying no user content, so the tricky
	// content above cannot false-positive the glyph absence assertions.
	chromeItems := []presentation.FeedItem{
		{Type: presentation.ItemAssistant, Text: ""},
		{Type: presentation.ItemError, Text: "boom"},
		{Type: presentation.ItemError, Text: strings.Repeat("e", 200)}, // forces truncation tail
		{Type: presentation.ItemNotice, Text: "hi"},
		{Type: presentation.ItemRunBoundary, Text: "s"},
		{Type: presentation.ItemPermission, Perm: &presentation.PermCard{Status: presentation.PermAllow, Name: "bash"}},
		{Type: presentation.ItemPermission, Perm: &presentation.PermCard{Status: presentation.PermDeny, Name: "bash"}},
	}
	var cb strings.Builder
	for _, line := range renderItems(chromeItems, width) {
		cb.WriteString(line)
		cb.WriteByte('\n')
	}
	chromeOut := cb.String()
	for _, glyph := range []string{"✗", "✓", "⚑", "──", "…", "·"} {
		if strings.Contains(chromeOut, glyph) {
			t.Errorf("ASCII mode emitted Unicode chrome %q", glyph)
		}
	}
	for _, want := range []string{"x ", "ok", "-- ", "...", "! "} {
		if !strings.Contains(chromeOut, want) {
			t.Errorf("ASCII mode missing expected ASCII chrome %q", want)
		}
	}
}

// Default (flag unset) rendering must keep the Unicode chrome.
func TestFeedRenderDefaultKeepsUnicodeChrome(t *testing.T) {
	t.Setenv("NABD_ASCII_ONLY", "")
	items := []presentation.FeedItem{
		{Type: presentation.ItemError, Text: "boom"},
		{Type: presentation.ItemNotice, Text: "hi"},
		{Type: presentation.ItemRunBoundary, Text: "s"},
		{Type: presentation.ItemPermission, Perm: &presentation.PermCard{Status: presentation.PermAllow, Name: "bash"}},
	}
	var sb strings.Builder
	for _, line := range renderItems(items, 80) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	out := sb.String()
	for _, want := range []string{"✗", "⚑", "──", "✓"} {
		if !strings.Contains(out, want) {
			t.Errorf("default mode missing Unicode chrome %q", want)
		}
	}
}
