package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// These tests pin the pointer contract that the keyboard already had:
// a tap never moves the content the user is looking at, never leaves the
// selection marker unpainted, and never leaves follow armed.

// TestTapOnSelectedCardDoesNotScrollTheViewport covers the expansion tap.
// refreshPreservingSelection pins the selected card to the top of the
// viewport, which is right for the keyboard (selectItem already put it
// there) and wrong for a finger pointing at a card mid-screen.
func TestTapOnSelectedCardDoesNotScrollTheViewport(t *testing.T) {
	// The feed must overflow the viewport, otherwise clampScroll pins
	// scrollTop at 0 and the bug is invisible.
	m := feedWithTools(t, 20, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	targetIdx := 10
	y := rowOfCard(m, targetIdx)
	if y < 0 {
		t.Fatalf("card %d not visible", targetIdx)
	}
	tap(m, y) // first tap: select in place

	items := m.navigationItems()
	cardID := items[targetIdx].ID

	y = rowOfCard(m, targetIdx)
	if y < 0 {
		t.Fatalf("card %d not visible after selection", targetIdx)
	}
	before := m.scrollTop

	tap(m, y) // second tap: expand

	if !m.effectiveExpanded(cardID) {
		t.Fatal("second tap on the selected card did not expand it")
	}
	if m.scrollTop != before {
		t.Fatalf("expand tap scrolled the viewport: before=%d after=%d", before, m.scrollTop)
	}
}

// TestTapOnSelectedCardKeepsItsRow is the stronger form: not only must
// scrollTop hold, the card must stay on the same screen row so nothing the
// user is looking at moves under the finger.
func TestTapOnSelectedCardKeepsItsRow(t *testing.T) {
	m := feedWithTools(t, 20, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	targetIdx := 10
	y := rowOfCard(m, targetIdx)
	if y < 0 {
		t.Fatalf("card %d not visible", targetIdx)
	}
	tap(m, y)

	items := m.navigationItems()
	cardID := items[targetIdx].ID

	yBefore := rowOfCard(m, targetIdx)
	if yBefore < 0 {
		t.Fatalf("card %d not visible after selection", targetIdx)
	}

	tap(m, yBefore)

	if !m.effectiveExpanded(cardID) {
		t.Fatal("second tap on the selected card did not expand it")
	}
	yAfter := rowOfCard(m, targetIdx)
	if yAfter != yBefore {
		t.Fatalf("card %d moved on screen: before row=%d after row=%d", targetIdx, yBefore, yAfter)
	}
}

// TestTapAfterLeavingNavigationRepaintsTheMarker covers the Esc path: Esc
// leaves navigation mode but keeps selectedItem, so re-entering by tapping
// the same card makes selectItemInPlace see prev == idx and skip the
// repaint, while navigationMode (which isSelected depends on) has just
// flipped. The marker must be painted.
func TestTapAfterLeavingNavigationRepaintsTheMarker(t *testing.T) {
	m := feedWithTools(t, 6, 60)
	m.touchEnabled = true
	m.height, m.follow = 20, false
	m.enterNavigation()
	m.selectItem(2)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // Esc keeps selectedItem == 2
	m.scrollTop = 0
	m.refresh()

	if m.navigationMode {
		t.Fatal("Esc must have left navigation mode")
	}

	y := rowOfCard(m, 2)
	if y < 0 {
		t.Fatal("card 2 not visible")
	}
	tap(m, y)

	if !m.navigationMode {
		t.Fatal("tap did not re-enter navigation mode")
	}
	lines := cardLines(m, 2)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "> ") {
		t.Fatalf("marker not painted on re-entry by tap: %q", lines)
	}
}

// TestTapStopsFollowing covers the third path: selectItemInPlace does not
// touch follow, so a tap while the feed is following leaves follow armed
// and the next refresh re-anchors to the bottom, dragging the picked card
// off screen.
func TestTapStopsFollowing(t *testing.T) {
	m := feedWithTools(t, 10, 60)
	m.touchEnabled = true
	m.height = 20
	m.scrollToEnd() // follow == true, viewport at the bottom
	m.refresh()

	if !m.follow {
		t.Fatal("test needs follow armed")
	}

	idx := len(m.navigationItems()) - 1
	y := rowOfCard(m, idx)
	if y < 0 {
		t.Fatalf("card %d not visible", idx)
	}
	tap(m, y)

	if m.follow {
		t.Fatal("tap left follow armed")
	}
}

// TestTapOnAnotherCardDoesNotScrollTheViewport is the control: selecting a
// different card has never scrolled, and must keep not scrolling.
func TestTapOnAnotherCardDoesNotScrollTheViewport(t *testing.T) {
	m := feedWithTools(t, 20, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	// Select card 1 first, then tap card 3.
	y1 := rowOfCard(m, 1)
	if y1 < 0 {
		t.Fatal("card 1 not visible")
	}
	tap(m, y1)

	y3 := rowOfCard(m, 3)
	if y3 < 0 {
		t.Fatal("card 3 not visible")
	}
	before := m.scrollTop
	tap(m, y3)

	if m.selectedItem != 3 {
		t.Fatalf("selectedItem = %d, want 3", m.selectedItem)
	}
	if m.scrollTop != before {
		t.Fatalf("selection tap scrolled: before=%d after=%d", before, m.scrollTop)
	}
}
