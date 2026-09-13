package ui

import (
	"strings"
	"testing"
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
