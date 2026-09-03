package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// TestViewOutputLayout prints the rendered layout for manual inspection and
// asserts structural invariants (viewport rows <= available height).
func TestViewOutputLayout(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 16
	_, _ = f.Update(tea.WindowSizeMsg{Width: 50, Height: 16})
	f.SetHeader("nabd test header")
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "session"},
		{Seq: 2, Type: agent.UserMsg, Text: "السؤال الأول"},
		{Seq: 3, Type: agent.TextDelta, Text: "الجواب الأول يجري كتابته هنا"},
		{Seq: 4, Type: agent.TurnEnd},
	}})
	typeIntoFeed(t, f, "رسالة متعددة")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "الأسطر")
	v := f.View()
	if v == "" {
		t.Fatal("empty view")
	}
	lines := countLines(v)
	if lines > f.height {
		t.Fatalf("view has %d lines but terminal height is %d:\n%s", lines, f.height, v)
	}
	t.Logf("layout (%d lines):\n%s", lines, v)

	// With a modal open, the help switches to permission keys.
	openModal(f)
	v2 := f.View()
	if v2 == "" {
		t.Fatal("empty modal view")
	}
	t.Logf("modal layout:\n%s", v2)
}

// TestViewPlaceholderOnEmptyComposer: the empty composer shows a clear
// placeholder in the rendered view.
func TestViewPlaceholderOnEmptyComposer(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 8
	_, _ = f.Update(tea.WindowSizeMsg{Width: 50, Height: 8})
	v := f.View()
	if !strings.Contains(v, "type a message") {
		t.Fatalf("empty composer must show a placeholder, got:\n%s", v)
	}
}

// TestViewSmallTerminalNoPanic: minimal terminal sizes render without
// panic and without negative viewport math.
func TestViewSmallTerminalNoPanic(t *testing.T) {
	for _, h := range []int{1, 2, 3, 4, 5} {
		f := NewFeed()
		_, _ = f.Update(tea.WindowSizeMsg{Width: 20, Height: h})
		f.SetHeader("h")
		_ = f.View()
	}
}

func countLines(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}
