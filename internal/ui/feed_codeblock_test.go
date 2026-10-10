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

// TestRenderAssistantHostileANSIWithFences verifies that hostile ANSI surrounding
// fences is sanitized before code block splitting, preventing ANSI escape leaks.
func TestRenderAssistantHostileANSIWithFences(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	resetRTLModeCache()
	defer resetRTLModeCache()

	item := presentation.FeedItem{
		Type: presentation.ItemAssistant,
		Text: "Intro\n\x1b[31m```go\x1b[0m\nfmt.Println(\"secure\")\n\x1b[32m```\x1b[0m\nOutro",
	}
	got := renderAssistant(item, 80)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "\x1b[31m") || strings.Contains(joined, "\x1b[32m") {
		t.Errorf("hostile ANSI survived renderAssistant: %q", joined)
	}
	if strings.Contains(joined, "```") {
		t.Errorf("fences not stripped after sanitization: %q", joined)
	}
	for _, want := range []string{"Intro", "fmt.Println", "Outro"} {
		if !strings.Contains(joined, want) {
			t.Errorf("lost readable text %q in: %q", want, joined)
		}
	}
}

// TestRenderAssistantWidthClamped verifies that non-positive widths are clamped
// to DefaultWidth instead of causing panic or invalid wrapping.
func TestRenderAssistantWidthClamped(t *testing.T) {
	item := presentation.FeedItem{
		Type: presentation.ItemAssistant,
		Text: "```\ncode here\n```\nprose",
	}
	got := renderAssistant(item, 0)
	if len(got) == 0 {
		t.Error("expected non-empty output with width=0")
	}
}
