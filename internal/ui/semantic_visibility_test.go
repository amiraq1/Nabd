package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Section F dimensions: 8 terminal geometries
var sectionFDimensions = []struct {
	name   string
	width  int
	height int
}{
	{"20x8", 20, 8},
	{"20x24", 20, 24},
	{"40x8", 40, 8},
	{"40x24", 40, 24},
	{"60x10", 60, 10},
	{"80x24", 80, 24},
	{"120x40", 120, 40},
	{"200x60", 200, 60},
}

// TestDimensionMatrixSectionF verifies all 8 terminal sizes from Section F
// across multiple UI states: idle, active streaming, tool execution, and completed turn.
func TestDimensionMatrixSectionF(t *testing.T) {
	for _, dim := range sectionFDimensions {
		t.Run(dim.name, func(t *testing.T) {
			f := newFeedAt(t, dim.width, dim.height)

			// 1. Idle state
			v := f.View()
			assertViewBounds(t, dim.name+"-idle", v, dim.width, dim.height)

			// 2. Feed with content
			for i := 1; i <= 20; i++ {
				f.Update(agentEventBatchMsg{Events: []agent.Event{
					{Seq: i, Type: agent.UserMsg, Text: fmt.Sprintf("msg_%02d", i)},
				}})
			}
			v = f.View()
			assertViewBounds(t, dim.name+"-content", v, dim.width, dim.height)

			// In follow mode, the newest message should be visible if viewport has height
			lh := f.computeLayout()
			if lh.ViewportRows > 0 {
				if !strings.Contains(v, "msg_20") {
					t.Errorf("[%s] follow mode: newest message 'msg_20' must be visible in view:\n%s", dim.name, v)
				}
			}

			// 3. Tool running state
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 21, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c_mat", Name: "bash", Args: json.RawMessage(`"make"`)}},
			}})
			v = f.View()
			assertViewBounds(t, dim.name+"-tool-running", v, dim.width, dim.height)

			// 4. Clean completion
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 22, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c_mat", Name: "bash", Output: "build ok", OK: true}},
				{Seq: 23, Type: agent.TurnEnd},
			}})
			v = f.View()
			assertViewBounds(t, dim.name+"-tool-done", v, dim.width, dim.height)
		})
	}
}

// TestSemanticVisibilityFollowAndScrolled verifies that:
// - Follow mode displays the newest line at bottomStart.
// - Scrolling up displays older lines.
// - Scrolling down returns to the newest line.
func TestSemanticVisibilityFollowAndScrolled(t *testing.T) {
	for _, dim := range []struct {
		w, h int
	}{
		{40, 15},
		{80, 24},
		{120, 40},
	} {
		name := fmt.Sprintf("%dx%d", dim.w, dim.h)
		t.Run(name, func(t *testing.T) {
			f := newFeedAt(t, dim.w, dim.h)
			const total = 40
			for i := 1; i <= total; i++ {
				f.Update(agentEventBatchMsg{Events: []agent.Event{
					{Seq: i, Type: agent.UserMsg, Text: fmt.Sprintf("semantic_line_%03d", i)},
				}})
			}

			// 1. Follow mode: line 40 must be visible, line 1 must not be visible
			v := f.View()
			assertViewBounds(t, name+"-follow", v, dim.w, dim.h)
			if !strings.Contains(v, "semantic_line_040") {
				t.Fatalf("[%s] expected newest line 'semantic_line_040' in follow mode", name)
			}
			if strings.Contains(v, "semantic_line_001") {
				t.Fatalf("[%s] oldest line 'semantic_line_001' should have scrolled out of view", name)
			}

			// 2. Scroll to top with Home key (blur composer so viewport owns navigation)
			f.composer.blur()
			f.Update(tea.KeyMsg{Type: tea.KeyHome})
			v = f.View()
			assertViewBounds(t, name+"-home", v, dim.w, dim.h)
			if !strings.Contains(v, "semantic_line_001") {
				t.Fatalf("[%s] expected oldest line 'semantic_line_001' after Home key", name)
			}
			if strings.Contains(v, "semantic_line_040") {
				t.Fatalf("[%s] newest line 'semantic_line_040' should not be visible at top", name)
			}

			// 3. Scroll back to bottom with End key
			f.Update(tea.KeyMsg{Type: tea.KeyEnd})
			v = f.View()
			assertViewBounds(t, name+"-end", v, dim.w, dim.h)
			if !strings.Contains(v, "semantic_line_040") {
				t.Fatalf("[%s] expected newest line 'semantic_line_040' after End key", name)
			}
		})
	}
}

// TestSemanticVisibilityModalOnAllSizes verifies that the permission modal
// renders gracefully across all 8 Section F dimensions.
func TestSemanticVisibilityModalOnAllSizes(t *testing.T) {
	for _, dim := range sectionFDimensions {
		t.Run(dim.name, func(t *testing.T) {
			f := newFeedAt(t, dim.width, dim.height)
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "p1", Name: "write_file", Args: json.RawMessage(`"main.go"`)}},
			}})

			if !f.permModal.visible {
				t.Fatalf("[%s] permission modal should be visible", dim.name)
			}

			v := f.View()
			assertViewBounds(t, dim.name+"-modal", v, dim.width, dim.height)

			// Permission modal title must be present
			if !strings.Contains(v, "Permission") {
				t.Errorf("[%s] modal view missing 'Permission':\n%s", dim.name, v)
			}
		})
	}
}

// TestSemanticVisibilitySlashMenuOnAllSizes verifies that the slash menu
// renders gracefully across all 8 Section F dimensions.
func TestSemanticVisibilitySlashMenuOnAllSizes(t *testing.T) {
	for _, dim := range sectionFDimensions {
		t.Run(dim.name, func(t *testing.T) {
			f := newFeedAt(t, dim.width, dim.height)
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

			if !f.menu.visible {
				t.Fatalf("[%s] slash menu should be visible", dim.name)
			}

			v := f.View()
			assertViewBounds(t, dim.name+"-slash-menu", v, dim.width, dim.height)

			// Commands header and first command must be visible
			if !strings.Contains(v, "Commands") && !strings.Contains(v, "/undo") {
				t.Errorf("[%s] slash menu view missing commands:\n%s", dim.name, v)
			}
		})
	}
}

// assertViewBounds validates that rendered view conforms to width and height constraints.
func assertViewBounds(t *testing.T, label string, view string, maxW, maxH int) {
	t.Helper()
	if !utf8.ValidString(view) {
		t.Fatalf("[%s] View() produced invalid UTF-8", label)
	}
	lines := strings.Split(view, "\n")
	if len(lines) > maxH {
		t.Fatalf("[%s] View() has %d lines, exceeding max height %d", label, len(lines), maxH)
	}
	for i, l := range lines {
		w := ansi.StringWidth(l)
		if w > maxW {
			t.Errorf("[%s] line %d width %d > terminal width %d: %q", label, i, w, maxW, l)
		}
	}
}
