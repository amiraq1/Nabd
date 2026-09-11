package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCommandHelpFitsWidths(t *testing.T) {
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		for _, line := range strings.Split(CommandHelp(width), "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d: %q", width, line)
			}
		}
	}
}

func TestSlashInvalidArgumentShowsUsage(t *testing.T) {
	for _, line := range []string{"/undo nope", "/ctx 2", "/rewind 1 extra"} {
		got := ParseSlashCommand(line)
		if got.Valid || !strings.Contains(got.Error, "usage:") {
			t.Fatalf("%q => %#v", line, got)
		}
	}
}

func TestFeedCardNavigationAndSemanticJumps(t *testing.T) {
	f := NewFeed()
	f.BuildFromEvents([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "one"},
		{Seq: 2, Type: agent.RunError, Err: "failure", ErrorCode: "unknown"},
		{Seq: 3, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "p", Name: "bash"}},
	})
	f.composer.clear()
	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !f.navigationMode {
		t.Fatal("Esc did not enter navigation")
	}
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if f.navigationItems()[f.selectedItem].Type != "error" {
		t.Fatal("n did not select error")
	}
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if f.navigationItems()[f.selectedItem].Type != "permission" {
		t.Fatal("p did not select permission")
	}
}

// The public contracts above intentionally cover both phone and desktop widths.
