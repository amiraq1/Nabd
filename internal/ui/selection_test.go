package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

func cardLines(m *Feed, idx int) []string {
	if idx < 0 || idx >= len(m.offsets) {
		return nil
	}
	start := m.offsets[idx]
	end := len(m.lines)
	if idx+1 < len(m.offsets) {
		end = m.offsets[idx+1]
	}
	if start > len(m.lines) || end > len(m.lines) || start > end {
		return nil
	}
	return m.lines[start:end]
}

func TestMarkerAppearsOnSelectedCard(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	m.enterNavigation()
	m.selectItem(1)
	m.refresh()

	lines := cardLines(m, 1)
	if len(lines) == 0 {
		t.Fatal("selected card has no lines")
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, "> ") {
			t.Fatalf("selected card line %d missing '> ' marker: %q", i, l)
		}
	}
}

func TestMarkerAppearsOnEveryLineOfSelectedCard(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	m.enterNavigation()
	m.selectItem(1)
	m.toggleCard(1)
	m.refreshPreservingSelection()

	lines := cardLines(m, 1)
	if len(lines) <= 1 {
		t.Fatalf("expected expanded card to have multiple lines, got %d", len(lines))
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, "> ") {
			t.Fatalf("expanded selected card line %d missing '> ' marker: %q", i, l)
		}
	}
}

func TestUnselectedCardsCarryTheSameGutter(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	m.enterNavigation()
	m.selectItem(1)
	m.refresh()

	for _, unselectedIdx := range []int{0, 2} {
		lines := cardLines(m, unselectedIdx)
		if len(lines) == 0 {
			t.Fatalf("unselected card %d has no lines", unselectedIdx)
		}
		for i, l := range lines {
			if !strings.HasPrefix(l, "  ") {
				t.Fatalf("unselected card %d line %d missing gutter prefix: %q", unselectedIdx, i, l)
			}
			if strings.HasPrefix(l, "> ") {
				t.Fatalf("unselected card %d line %d erroneously marked with '> ': %q", unselectedIdx, i, l)
			}
		}
	}
}

func TestSelectionDoesNotChangeCardHeight(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	before := itemRowCount(m, 1)

	m.enterNavigation()
	m.selectItem(1)
	m.refresh()
	after := itemRowCount(m, 1)

	if before != after {
		t.Fatalf("selection changed card height: %d -> %d", before, after)
	}
}

func TestSelectionNeverExceedsWidth(t *testing.T) {
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		m := feedWithTools(t, 3, width)
		m.enterNavigation()
		m.selectItem(0)
		m.refresh()
		for i, line := range m.lines {
			if lineWidth(line) > width {
				t.Fatalf("width=%d: row %d is %d wide (exceeds width)", width, i, lineWidth(line))
			}
		}
	}
}

func TestSelectionIsCacheKeyNotInvalidator(t *testing.T) {
	m := feedWithTools(t, 20, 60)
	m.enterNavigation()
	m.selectItem(5)
	m.refresh()
	before := m.renderCount
	m.moveCard(1)
	m.refresh()
	delta := m.renderCount - before
	t.Logf("actual render delta: %d", delta)
	if delta > 2 {
		t.Fatalf("moving the cursor re-rendered %d cards, want <= 2", delta)
	}
}

func TestMovingSelectionKeepsOffsetsConsistent(t *testing.T) {
	m := feedWithTools(t, 5, 60)
	m.enterNavigation()
	m.selectItem(0)
	m.refresh()
	m.moveCard(2)
	m.refresh()

	for i := 0; i < len(m.offsets)-1; i++ {
		if m.offsets[i] > m.offsets[i+1] {
			t.Fatalf("offsets not non-decreasing: offset[%d]=%d > offset[%d]=%d",
				i, m.offsets[i], i+1, m.offsets[i+1])
		}
	}
}

func TestSelectionMarkerIsAsciiOnly(t *testing.T) {
	for _, selected := range []bool{true, false} {
		p := selectionPrefix(selected)
		for i := 0; i < len(p); i++ {
			if p[i] >= 128 {
				t.Fatalf("selectionPrefix(%v) contains non-ASCII byte: %d", selected, p[i])
			}
		}
	}
}

func TestScrollPositionHiddenWhenEverythingFits(t *testing.T) {
	m := feedWithTools(t, 2, 60)
	// Viewport is large enough to fit all rendered lines
	if pos := m.scrollPositionText(len(m.lines) + 5); pos != "" {
		t.Fatalf("expected empty position when everything fits, got %q", pos)
	}
	if pos := m.scrollPositionText(len(m.lines)); pos != "" {
		t.Fatalf("expected empty position when viewport equals line count, got %q", pos)
	}
}

func TestScrollPositionSurvivesLiveRun(t *testing.T) {
	m := feedWithTools(t, 8, 80)
	m.running = true
	m.busy = true
	m.height = 10 // small enough so viewportRows < total lines
	lm := m.computeLayout()
	if !strings.Contains(lm.runtimeStatusLine, "/") {
		t.Fatalf("scroll position missing during live run: %q", lm.runtimeStatusLine)
	}
	if !strings.Contains(lm.runtimeStatusLine, "Generating") {
		t.Fatalf("live run status missing: %q", lm.runtimeStatusLine)
	}
}

func TestPositionNeverDisplacesRunStatus(t *testing.T) {
	m := feedWithTools(t, 8, 20)
	m.running = true
	m.busy = true
	m.height = 10
	m.statusProj.Apply(agent.Event{
		Type: agent.ToolStart,
		Call: &agent.ToolCall{ID: "c1", Name: "read_file"},
	})
	lm := m.computeLayout()
	if !strings.Contains(lm.runtimeStatusLine, "Running read_file") {
		t.Fatalf("status line did not keep run status on narrow width: %q", lm.runtimeStatusLine)
	}
	if strings.Contains(lm.runtimeStatusLine, "/") {
		t.Fatalf("scroll position displaced run status on width 20: %q", lm.runtimeStatusLine)
	}
}

func TestNavigationModeIsVisibleWhenIdle(t *testing.T) {
	m := NewFeed()
	m.width, m.height = 80, 24
	m.enterNavigation()
	if got := m.runtimeStatusText(); !strings.Contains(got, "browsing") {
		t.Fatalf("expected browsing in status when idle, got %q", got)
	}
	lm := m.computeLayout()
	if !strings.Contains(lm.runtimeStatusLine, "browsing") {
		t.Fatalf("expected browsing in runtimeStatusLine when idle, got %q", lm.runtimeStatusLine)
	}
}

// These tests deliberately never call m.refresh(): the production path must
// do it. The existing selection tests pass only because they refresh by hand.

func TestMarkerFollowsCursorThroughUpdate(t *testing.T) {
	m := feedWithTools(t, 5, 60)
	m.enterNavigation()
	m.selectItem(1)
	m.refresh()

	m.Update(tea.KeyMsg{Type: tea.KeyDown})

	if lines := cardLines(m, 1); len(lines) > 0 && strings.HasPrefix(lines[0], "> ") {
		t.Fatal("card 1 kept the marker after the cursor left it")
	}
	lines := cardLines(m, 2)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "> ") {
		t.Fatalf("card 2 did not gain the marker: %q", lines)
	}
}

func TestEnteringNavigationPaintsTheMarker(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	m.selectItem(2) // already on the card enterNavigation would pick
	m.enterNavigation()
	lines := cardLines(m, 2)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "> ") {
		t.Fatalf("marker absent on entering navigation: %q", lines)
	}
}

func TestLeavingNavigationRetractsTheMarker(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	m.enterNavigation()
	m.selectItem(1)
	m.refresh()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	for _, l := range m.lines {
		if strings.HasPrefix(l, "> ") {
			t.Fatalf("marker survived leaving navigation: %q", l)
		}
	}
}

func TestHelpKeyShowsHintOnFirstPress(t *testing.T) {
	m := feedWithTools(t, 3, 80)
	m.enterNavigation()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.status == "" {
		t.Fatal("first '?' cleared the status instead of showing the hint")
	}
}

func TestPositionHiddenWhenViewportIsZero(t *testing.T) {
	m := feedWithTools(t, 8, 60)
	if pos := m.scrollPositionText(0); pos != "" {
		t.Fatalf("position reported for a zero-row viewport: %q", pos)
	}
}

func TestCursorSweepStaysWithinRenderBudget(t *testing.T) {
	// A full sweep must cost ~2 repaints per step, not a full re-render.
	m := feedWithTools(t, 20, 60)
	m.enterNavigation()
	m.selectItem(0)
	m.refresh()
	before := m.renderCount
	for i := 0; i < 10; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	delta := m.renderCount - before
	t.Logf("actual sweep render delta: %d", delta)
	if delta > 20 {
		t.Fatalf("10 cursor steps repainted %d cards, want <= 20", delta)
	}
}
