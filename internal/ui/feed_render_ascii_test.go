package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

// NABD_ASCII_ONLY must replace decorative feed glyphs with ASCII equivalents
// while preserving content text (including Arabic and Unicode punctuation
// inside user/model text). See docs/CONFIG.md.
func TestFeedRenderASCIIOnlyReplacesChrome(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	resetRTLModeCache()
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

// Tool cards, expanded tool output, and the renderer-generated retention
// notice are chrome too: they must obey NABD_ASCII_ONLY like the rest of the
// feed. The summary separator/tails and the hidden-lines markers are the
// sites the original ASCII coverage missed.
func TestFeedRenderASCIIOnlyToolChromeAndRetentionNotice(t *testing.T) {
	t.Setenv("NABD_ASCII_ONLY", "1")
	width := 80

	var bashOutput strings.Builder
	for i := 0; i < maxToolOutputLines+20; i++ {
		fmt.Fprintf(&bashOutput, "line %d\n", i)
	}
	var readOutput strings.Builder
	for i := 1; i <= maxToolOutputLines+20; i++ {
		fmt.Fprintf(&readOutput, "%d|content %d\n", i, i)
	}

	longArgs := strings.Repeat("a", 200)
	tools := []presentation.FeedItem{
		// Two metadata entries on a line that still fits join with the separator.
		{Type: presentation.ItemTool, Tool: &presentation.ToolCard{
			Name: "bash", Args: "echo hi", Status: presentation.ToolDone,
			Truncated: true, OutputState: presentation.OutputTruncated,
		}},
		// A long subject with important metadata falls back to the separator
		// form of the summary line.
		{Type: presentation.ItemTool, Tool: &presentation.ToolCard{
			Name: "bash", Args: longArgs, Status: presentation.ToolRunning,
		}},
		// A long subject with no important metadata takes the truncation tail.
		{Type: presentation.ItemTool, Tool: &presentation.ToolCard{
			Name: "bash", Args: longArgs, Status: presentation.ToolDone,
		}},
		// Expanded non-read output reaches truncateOutput's hidden-lines marker.
		{Type: presentation.ItemTool, Tool: &presentation.ToolCard{
			Name: "bash", Status: presentation.ToolDone,
			Output: bashOutput.String(), OutputState: presentation.OutputSaved,
		}},
		// Expanded read_file output reaches renderReadFileOutput's marker.
		{Type: presentation.ItemTool, Tool: &presentation.ToolCard{
			Name: "read_file", Status: presentation.ToolDone,
			Output: readOutput.String(), OutputState: presentation.OutputSaved,
		}},
	}

	var sb strings.Builder
	for _, line := range renderItems(tools, width, true) {
		if got := ansi.StringWidth(line); got > width {
			t.Errorf("line exceeds width %d: %d cells: %q", width, got, line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	out := sb.String()

	for _, glyph := range []string{"✗", "✓", "⚑", "──", "…", "·"} {
		if strings.Contains(out, glyph) {
			t.Errorf("ASCII mode emitted Unicode chrome %q in tool rendering", glyph)
		}
	}
	for _, want := range []string{"truncated", " - ", "...", "lines / ", "chars hidden ..."} {
		if !strings.Contains(out, want) {
			t.Errorf("ASCII mode missing expected ASCII chrome %q in tool rendering", want)
		}
	}
	if got := strings.Count(out, "lines / "); got != 2 {
		t.Errorf("hidden-lines marker count = %d, want 2 (truncateOutput + renderReadFileOutput)", got)
	}

	// The retention notice is generated by the renderer, so it must be mapped
	// too; it must stay unchanged when the flag is unset.
	retained := visibleFeedItems(make([]presentation.FeedItem, maxVisibleFeedItems+1))
	if len(retained) != maxVisibleFeedItems {
		t.Fatalf("retained %d items, want %d", len(retained), maxVisibleFeedItems)
	}
	asciiNotice := strings.Join(renderItem(retained[0], width), "\n")
	if !strings.Contains(asciiNotice, "... 2 older items hidden - session journal has full history ...") {
		t.Errorf("retention notice did not map to ASCII chrome: %q", asciiNotice)
	}
	t.Setenv("NABD_ASCII_ONLY", "")
	// The notice text is baked at generation time: regenerate it to assert
	// the default (flag unset) output stays byte-identical.
	retainedDefault := visibleFeedItems(make([]presentation.FeedItem, maxVisibleFeedItems+1))
	unicodeNotice := strings.Join(renderItem(retainedDefault[0], width), "\n")
	if !strings.Contains(unicodeNotice, "… 2 older items hidden · session journal has full history …") {
		t.Errorf("default retention notice changed: %q", unicodeNotice)
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
