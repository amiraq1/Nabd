package ui

import (
	"strings"
	"testing"

	"nabd/internal/presentation"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestRenderAssistantStripsCodeFences verifies the Feed UI hides ``` fences
// and renders code with dark background, matching Chat UI behavior.
func TestRenderAssistantStripsCodeFences(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	item := presentation.FeedItem{
		Type: presentation.ItemAssistant,
		Text: "```python\ndef add(a, b):\n    return a + b\n```\n\nDone.",
	}
	got := renderAssistant(item, 50)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "```") {
		t.Errorf("code fences not stripped in Feed UI, got:\n%s", joined)
	}
	if !strings.Contains(joined, "def add(a, b):") {
		t.Errorf("code content missing, got:\n%s", joined)
	}
}

// TestRenderAssistantCodeHasDarkBackground verifies code blocks use the
// dark background style (ANSI 236) in Feed UI.
func TestRenderAssistantCodeHasDarkBackground(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	item := presentation.FeedItem{
		Type: presentation.ItemAssistant,
		Text: "```\ncode here\n```",
	}
	got := renderAssistant(item, 50)
	joined := strings.Join(got, "\n")
	// ANSI 236 background = \x1b[48;5;236m
	if !strings.Contains(joined, "48;5;236") {
		t.Errorf("code block missing dark background (ANSI 236), got:\n%q", joined)
	}
}
