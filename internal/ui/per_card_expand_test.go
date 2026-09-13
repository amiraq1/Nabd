package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

func feedWithTools(t *testing.T, count, width int) *Feed {
	t.Helper()
	m := NewFeed()
	m.width = width
	m.height = 10
	events := make([]agent.Event, 0, count*2)
	for i := 0; i < count; i++ {
		lines := []string{
			fmt.Sprintf("output line 1 for tool %d", i),
			fmt.Sprintf("output line 2 for tool %d", i),
			fmt.Sprintf("output line 3 for tool %d", i),
			fmt.Sprintf("output line 4 for tool %d", i),
			fmt.Sprintf("output line 5 for tool %d", i),
			fmt.Sprintf("output line 6 for tool %d", i),
			fmt.Sprintf("output line 7 for tool %d", i),
			fmt.Sprintf("output line 8 for tool %d", i),
		}
		events = append(events,
			agent.Event{
				Seq:  i*2 + 1,
				Type: agent.ToolStart,
				Call: &agent.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "bash", Args: []byte(`"echo test"`)},
			},
			agent.Event{
				Seq:  i*2 + 2,
				Type: agent.ToolEnd,
				Call: &agent.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "bash", Output: strings.Join(lines, "\n"), OK: true},
			},
		)
	}
	m.applyBatch(events)
	m.refresh()
	return m
}

func toolIndexes(m *Feed) []int {
	items := m.navigationItems()
	var idxs []int
	for i, it := range items {
		if expandable(it) {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

func itemRowCount(m *Feed, idx int) int {
	if idx < 0 || idx >= len(m.offsets) {
		return 0
	}
	end := len(m.lines)
	if idx+1 < len(m.offsets) {
		end = m.offsets[idx+1]
	}
	return end - m.offsets[idx]
}

func TestToggleCardExpandsTargetOnly(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	tools := toolIndexes(m)
	target := tools[0]
	other := tools[1]
	beforeTarget := itemRowCount(m, target)
	beforeOther := itemRowCount(m, other)

	m.selectItem(target)
	m.toggleCard(target)
	m.refreshPreservingSelection()

	if got := itemRowCount(m, target); got <= beforeTarget {
		t.Fatalf("target card did not expand: %d -> %d", beforeTarget, got)
	}
	if got := itemRowCount(m, other); got != beforeOther {
		t.Fatalf("unselected card changed height: %d -> %d", beforeOther, got)
	}
}

func TestToggleAllClearsPerCardOverrides(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	tools := toolIndexes(m)
	m.selectItem(tools[0])
	m.toggleCard(tools[0])
	m.refreshPreservingSelection()
	if len(m.overrides) == 0 {
		t.Fatal("override not recorded")
	}

	if _, _ = m.toggleTools(); len(m.overrides) != 0 {
		t.Fatalf("Ctrl+O left %d overrides behind", len(m.overrides))
	}
	// Global expand must now govern every card uniformly.
	first := itemRowCount(m, tools[0])
	for _, idx := range tools[1:] {
		if got := itemRowCount(m, idx); got != first {
			t.Fatalf("cards not uniform after global toggle: %d vs %d", first, got)
		}
	}
}

// Collapse-all must be able to collapse a hand-expanded card.
func TestGlobalCollapseOverridesHandExpansion(t *testing.T) {
	m := feedWithTools(t, 2, 60)
	tools := toolIndexes(m)
	base := itemRowCount(m, tools[0])

	m.selectItem(tools[0])
	m.toggleCard(tools[0])
	m.refreshPreservingSelection()
	expanded := itemRowCount(m, tools[0])
	if expanded <= base {
		t.Fatalf("hand-expansion did not expand card: %d -> %d", base, expanded)
	}

	// Ctrl+O twice: expand all, then collapse all.
	m.toggleTools()
	m.toggleTools()
	if got := itemRowCount(m, tools[0]); got != base {
		t.Fatalf("collapse all failed to collapse hand-expanded card: %d, want %d", got, base)
	}
}

func TestCacheKeyIsolatesPerCardExpansion(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	tools := toolIndexes(m)
	m.selectItem(tools[1])
	m.toggleCard(tools[1])
	m.refreshPreservingSelection()

	// The cached lines for each card must match a fresh render of that card
	// under its own effective flag, not under a shared one.
	for _, it := range m.navigationItems() {
		if !expandable(it) {
			continue
		}
		want := renderItem(it, m.width, m.effectiveExpanded(it.ID))
		got := m.lineCache[it.ID].lines
		if len(got) != len(want) {
			t.Fatalf("card %s: cached %d rows, fresh render %d", it.ID, len(got), len(want))
		}
	}
}

func TestExpansionKeepsSelectedCardAnchored(t *testing.T) {
	m := feedWithTools(t, 8, 60)
	tools := toolIndexes(m)
	mid := tools[len(tools)/2]
	m.selectItem(mid)
	m.follow = false

	m.toggleCard(mid)
	m.refreshPreservingSelection()

	if m.scrollTop != m.offsets[mid] {
		t.Fatalf("selected card not anchored: scrollTop=%d, offset=%d", m.scrollTop, m.offsets[mid])
	}
}

func TestNonToolCardIsNotExpandable(t *testing.T) {
	m := NewFeed()
	m.width, m.height = 60, 24
	m.lastSeq = 1
	m.addNotice(presentation.ItemNotice, "a notice")
	m.refresh()
	for i, it := range m.navigationItems() {
		if expandable(it) {
			continue
		}
		if m.toggleCard(i) {
			t.Fatalf("toggleCard accepted a non-expandable item of type %s", it.Type)
		}
	}
}

func TestOverridesPrunedWithTrimmedItems(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	m.overrides = map[string]bool{"ghost-id": true}
	m.refresh()
	if _, ok := m.overrides["ghost-id"]; ok {
		t.Fatal("override for a vanished card was not pruned")
	}
}

// Expansion is display state only: it must not touch the journal or the
// projected item set.
func TestExpansionDoesNotMutateProjection(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	before := m.proj.Items()
	fps := make([]uint64, len(before))
	for i, it := range before {
		fps[i] = it.Fingerprint()
	}

	tools := toolIndexes(m)
	m.selectItem(tools[0])
	m.toggleCard(tools[0])
	m.refreshPreservingSelection()
	m.toggleTools()

	after := m.proj.Items()
	if len(after) != len(before) {
		t.Fatalf("item count changed: %d -> %d", len(before), len(after))
	}
	for i, it := range after {
		if it.Fingerprint() != fps[i] {
			t.Fatalf("item %d mutated by expansion", i)
		}
	}
}

func TestExpansionRespectsWidthAtEveryWidth(t *testing.T) {
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		m := feedWithTools(t, 3, width)
		tools := toolIndexes(m)
		m.selectItem(tools[0])
		m.toggleCard(tools[0])
		m.refreshPreservingSelection()
		for i, line := range m.lines {
			if lineWidth(line) > width {
				t.Fatalf("width=%d: row %d is %d wide", width, i, lineWidth(line))
			}
		}
	}
}

func TestNavigationEnterAndSpaceToggleCard(t *testing.T) {
	m := feedWithTools(t, 3, 60)
	tools := toolIndexes(m)
	m.enterNavigation()
	m.selectItem(tools[0])
	base := itemRowCount(m, tools[0])

	// Enter toggles expand
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := itemRowCount(m, tools[0]); got <= base {
		t.Fatalf("Enter failed to expand card: %d -> %d", base, got)
	}

	// Space toggles collapse back
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if got := itemRowCount(m, tools[0]); got != base {
		t.Fatalf("Space failed to collapse card back: %d, want %d", got, base)
	}
}

func TestNavigationFooterNeverAdvertisesSend(t *testing.T) {
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		m := feedWithTools(t, 3, width)
		m.enterNavigation()
		foot := m.footerText(width)
		if strings.Contains(foot, "send") {
			t.Fatalf("width=%d: navigation footer still advertises send: %q", width, foot)
		}
		if lineWidth(foot) > width {
			t.Fatalf("width=%d: footer is %d wide", width, lineWidth(foot))
		}
	}
}

func TestNavigationFooterAdvertisesExpand(t *testing.T) {
	// At every width that can hold it, the rebound key must be discoverable.
	for _, width := range []int{40, 79, 80, 120} {
		m := feedWithTools(t, 3, width)
		m.enterNavigation()
		if foot := m.footerText(width); !strings.Contains(foot, "expand") {
			t.Fatalf("width=%d: expand not discoverable: %q", width, foot)
		}
	}
}

func TestEnterOnNonExpandableCardExplainsItself(t *testing.T) {
	m := NewFeed()
	m.width, m.height = 60, 24
	m.lastSeq = 1
	m.addNotice(presentation.ItemNotice, "a notice")
	m.enterNavigation()
	m.selectItem(0)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.status == "" {
		t.Fatal("Enter on a non-expandable card gave no feedback")
	}
}
