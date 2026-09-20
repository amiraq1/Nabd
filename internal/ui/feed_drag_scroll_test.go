package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// dragFeed builds a touch-enabled feed with n synthetic lines, anchored at the
// bottom and following, and returns it with the layout already computed.
func dragFeed(t *testing.T, n int) (*Feed, layoutMetrics) {
	t.Helper()
	t.Setenv("NABD_NO_MOUSE", "")
	f := NewFeed()
	f.SetTouch(true)
	f.width = 80
	f.height = 24
	lines := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("Line %02d", i))
	}
	f.lines = lines
	lm := f.computeLayout()
	f.scrollTop = f.bottomStart(lm.ViewportRows)
	f.follow = true
	return f, lm
}

// TestFeed_TouchDragScrollsViewport verifies that a press followed by vertical
// motion scrolls the viewport by the motion delta, and that releasing never
// resolves as a tap.
func TestFeed_TouchDragScrollsViewport(t *testing.T) {
	f, lm := dragFeed(t, 40)
	bs := f.bottomStart(lm.ViewportRows)
	if bs <= 6 {
		t.Fatalf("vacuous setup: bottomStart %d too small to observe a drag", bs)
	}

	startY := lm.viewportTop() + lm.ViewportRows/2
	_, _ = f.Update(tea.MouseMsg{X: 10, Y: startY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})

	// Finger down by 5 rows reveals older content and stops following.
	_, _ = f.Update(tea.MouseMsg{X: 10, Y: startY + 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	if f.follow {
		t.Errorf("drag down must disable follow")
	}
	if f.scrollTop != bs-5 {
		t.Fatalf("after dragging down 5: scrollTop = %d, want %d", f.scrollTop, bs-5)
	}

	// A long continued drag clamps at the top.
	_, _ = f.Update(tea.MouseMsg{X: 10, Y: startY + 100, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	if f.scrollTop != 0 {
		t.Fatalf("dragging past the top must clamp to 0, got %d", f.scrollTop)
	}

	// Dragging back up returns to the exact bottom and re-arms follow.
	_, _ = f.Update(tea.MouseMsg{X: 10, Y: startY - 100, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	if f.scrollTop != bs {
		t.Fatalf("dragging up must clamp to bottomStart %d, got %d", bs, f.scrollTop)
	}
	if !f.follow {
		t.Errorf("reaching the bottom by drag must re-arm follow")
	}
	if f.unseen != 0 {
		t.Errorf("reaching the bottom must clear unseen, got %d", f.unseen)
	}

	// Release must not tap: a drag is never a tap.
	_, _ = f.Update(tea.MouseMsg{X: 10, Y: startY, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if f.pointerDown || f.pointerDragged {
		t.Errorf("release must clear pointer state: down=%v dragged=%v", f.pointerDown, f.pointerDragged)
	}
}

// TestFeed_DragNeverTriggersTap verifies that a press/motion/release sequence
// over a card changes neither the selection nor the navigation mode.
func TestFeed_DragNeverTriggersTap(t *testing.T) {
	f := newFeedAt(t, 80, 20)
	populateFeedWithLines(f, 40)
	f.SetTouch(true)
	t.Setenv("NABD_NO_MOUSE", "")
	f.composer.blur()
	f.Update(tea.MouseMsg{Action: tea.MouseActionMotion, Y: 0}) // ensure no stale state

	lm := f.computeLayout()
	startY := lm.viewportTop() + 1
	if idx := f.itemAt(f.scrollTop + 1); idx < 0 {
		t.Fatal("vacuous setup: no item under the drag start")
	} else {
		f.selectItemInPlace(idx)
	}
	before := f.selectedItem
	modeBefore := f.navigationMode

	_, _ = f.Update(tea.MouseMsg{X: 10, Y: startY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	_, _ = f.Update(tea.MouseMsg{X: 10, Y: startY - 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	_, _ = f.Update(tea.MouseMsg{X: 10, Y: startY - 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})

	if f.selectedItem != before {
		t.Fatalf("a drag changed the selection: %d -> %d", before, f.selectedItem)
	}
	if f.navigationMode != modeBefore {
		t.Fatalf("a drag changed navigation mode: %v -> %v", modeBefore, f.navigationMode)
	}
}

// TestFeed_PgUpPgDnWorkWhileComposerFocused verifies that the explicit scroll
// keys reach the viewport even while the composer owns focus.
func TestFeed_PgUpPgDnWorkWhileComposerFocused(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 40)
	f.follow = true

	if !f.composer.focused() {
		t.Fatal("precondition: composer must be focused")
	}

	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if f.follow {
		t.Fatalf("PgUp must disable follow while the composer is focused")
	}
	if f.scrollTop == 0 {
		t.Fatalf("PgUp must move scrollTop away from the bottom")
	}

	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if !f.follow {
		t.Fatalf("PgDown must re-arm follow when it reaches the bottom")
	}
}

// TestFeed_HomeEndAreNavigationKeys verifies that Home/End scroll outside
// navigation mode but move the card selection inside it (Esc required first).
func TestFeed_HomeEndAreNavigationKeys(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 40)

	// Outside navigation mode, Home/End scroll the viewport.
	f.composer.blur()
	f.Update(tea.KeyMsg{Type: tea.KeyHome})
	if f.scrollTop != 0 {
		t.Fatalf("Home outside navigation must scroll to top, got %d", f.scrollTop)
	}
	f.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !f.follow {
		t.Fatalf("End outside navigation must re-arm follow")
	}

	// Inside navigation mode, Home/End move the selection instead.
	f.enterNavigation()
	f.Update(tea.KeyMsg{Type: tea.KeyHome})
	if f.selectedItem != 0 {
		t.Fatalf("Home in navigation must select the first card, got %d", f.selectedItem)
	}
	f.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if want := len(f.navigationItems()) - 1; f.selectedItem != want {
		t.Fatalf("End in navigation must select the last card, got %d, want %d", f.selectedItem, want)
	}
	if !f.follow {
		t.Fatalf("End in navigation must re-arm follow")
	}
}
