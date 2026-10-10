package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestRenderAgentTextHighlightsCodeBlocks verifies fenced code blocks
// get distinct styling from regular text.
func TestRenderAgentTextHighlightsCodeBlocks(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	input := "Here is code:\n```go\nfmt.Println(\"hi\")\n```\nDone."
	got := renderAgentText(input, 80)
	// Code content must appear with different styling than surrounding text.
	// We check the code fence markers are gone and code has background styling.
	if strings.Contains(got, "```") {
		t.Fatalf("code fences should be processed, got %q", got)
	}
	if !strings.Contains(got, "fmt.Println") {
		t.Fatalf("code content missing, got %q", got)
	}
	// Should contain ANSI codes for the code block background.
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("no styling applied, got %q", got)
	}
}

// TestRenderAgentTextNoCodeBlocks passes through plain text unchanged in structure.
func TestRenderAgentTextNoCodeBlocks(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	input := "Just plain text, no code here."
	got := renderAgentText(input, 80)
	if !strings.Contains(got, "Just plain text") {
		t.Fatalf("plain text altered, got %q", got)
	}
}

// TestRenderAgentTextArabicProseReorders verifies Arabic prose outside
// code fences goes through RTL reordering (visual order).
func TestRenderAgentTextArabicProseReorders(t *testing.T) {
	t.Setenv("NABD_RTL", "reorder")
	resetRTLModeCache()
	defer resetRTLModeCache()

	input := "مرحبا بالعالم"
	got := renderAgentText(input, 80)
	// Reordered: visual order differs from logical input.
	if strings.Contains(got, "مرحبا بالعالم") {
		t.Fatalf("Arabic prose was not reordered, got %q", got)
	}
	if !strings.Contains(got, "ابحرم") {
		t.Fatalf("expected visual order, got %q", got)
	}
}

// TestRenderAgentTextArabicInCodeStaysLTR verifies Arabic inside a
// ``` fence is NOT reordered: code is direction-neutral, reordering
// would corrupt identifiers and strings.
func TestRenderAgentTextArabicInCodeStaysLTR(t *testing.T) {
	t.Setenv("NABD_RTL", "reorder")
	resetRTLModeCache()
	defer resetRTLModeCache()

	input := "```\nname = \"مرحبا\"\n```"
	got := renderAgentText(input, 80)
	// The Arabic string literal inside code must stay in logical order.
	if !strings.Contains(got, "مرحبا") {
		t.Fatalf("Arabic in code was reordered, got %q", got)
	}
}
