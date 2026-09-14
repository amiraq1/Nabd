package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

func tap(m *Feed, y int) (tea.Model, tea.Cmd) {
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: y})
	return m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 1, Y: y})
}

func rowOfCard(m *Feed, idx int) int {
	lm := m.computeLayout()
	if idx < 0 || idx >= len(m.offsets) {
		return -1
	}
	line := m.offsets[idx]
	top := lm.viewportTop()
	if line < m.scrollTop || line >= m.scrollTop+lm.ViewportRows {
		return -1
	}
	return top + (line - m.scrollTop)
}

func feedWithPendingPermission(t *testing.T, width int) *Feed {
	t.Helper()
	m := feedWithTools(t, 4, width)
	m.touchEnabled = true
	m.height = 24
	m.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 100, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c_perm", Name: "bash"}},
	}})
	if !m.modalVisible {
		t.Fatal("modal must be visible")
	}
	return m
}

func TestViewportTopMatchesRenderedRowOrder(t *testing.T) {
	// The pointer maps a screen row to a feed line. If viewportTop and the
	// row order in View ever disagree, every tap selects a neighbouring
	// card and no other test would notice.
	m := feedWithTools(t, 6, 60)
	m.height = 24
	m.setStatus("browsing", rankHint) // force the status row to exist
	lm := m.computeLayout()
	if lm.RuntimeStatusRows == 0 {
		t.Fatal("test needs a visible status row")
	}
	rows := strings.Split(m.View(), "\n")
	top := lm.viewportTop()
	// When following with short content, viewportTopPadding places blank
	// rows before the feed lines. The first feed line is at top+padding,
	// not at top itself.
	topPad := m.viewportTopPadding(lm)
	if top+topPad >= len(rows) {
		t.Fatalf("viewportTop+padding %d outside the rendered view (%d rows)", top+topPad, len(rows))
	}
	want := m.lines[m.scrollTop]
	if got := rows[top+topPad]; got != want {
		t.Fatalf("row %d is %q, but scrollTop line is %q", top+topPad, got, want)
	}
}

func TestTapSelectsTheCardUnderTheFinger(t *testing.T) {
	m := feedWithTools(t, 5, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	targetIdx := 2
	y := rowOfCard(m, targetIdx)
	if y < 0 {
		t.Fatalf("card %d not visible in viewport", targetIdx)
	}

	tap(m, y)

	if m.selectedItem != targetIdx {
		t.Fatalf("selectedItem = %d, want %d", m.selectedItem, targetIdx)
	}
}

func TestTapDoesNotScrollTheViewport(t *testing.T) {
	m := feedWithTools(t, 10, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	targetIdx := 1
	y := rowOfCard(m, targetIdx)
	if y < 0 {
		t.Fatalf("card %d not visible", targetIdx)
	}
	beforeScroll := m.scrollTop

	tap(m, y)

	if m.scrollTop != beforeScroll {
		t.Fatalf("scrollTop changed from %d to %d after tap", beforeScroll, m.scrollTop)
	}
	if m.selectedItem != targetIdx {
		t.Fatalf("selectedItem = %d, want %d", m.selectedItem, targetIdx)
	}
}

func TestTapOnSelectedCardExpandsIt(t *testing.T) {
	m := feedWithTools(t, 4, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	y := rowOfCard(m, 1)
	if y < 0 {
		t.Fatal("card 1 not visible")
	}

	// First tap selects card 1 and enters navigation mode
	tap(m, y)
	if m.selectedItem != 1 {
		t.Fatalf("selectedItem = %d, want 1", m.selectedItem)
	}

	items := m.navigationItems()
	cardID := items[1].ID
	if m.effectiveExpanded(cardID) {
		t.Fatal("card 1 unexpectedly expanded on first tap")
	}

	// Second tap on the already-selected card toggles its expansion
	y = rowOfCard(m, 1)
	if y < 0 {
		t.Fatal("card 1 not visible after selection")
	}
	tap(m, y)

	if !m.effectiveExpanded(cardID) {
		t.Fatal("second tap on selected card did not expand it")
	}

	// Third tap collapses it again
	y = rowOfCard(m, 1)
	if y < 0 {
		t.Fatal("card 1 not visible after expansion")
	}
	tap(m, y)

	if m.effectiveExpanded(cardID) {
		t.Fatal("third tap on selected card did not collapse it")
	}
}

func TestTapEntersNavigationSoTheMarkerShows(t *testing.T) {
	m := feedWithTools(t, 4, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	if m.navigationMode {
		t.Fatal("expected feed not in navigation mode initially")
	}

	targetIdx := 1
	y := rowOfCard(m, targetIdx)
	if y < 0 {
		t.Fatalf("card %d not visible", targetIdx)
	}
	tap(m, y)

	if !m.navigationMode {
		t.Fatal("tap did not enter navigation mode")
	}
	lines := cardLines(m, targetIdx)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "> ") {
		t.Fatalf("card %d missing selection marker after tap: %q", targetIdx, lines)
	}
}

func TestTapOnChromeIsIgnored(t *testing.T) {
	m := feedWithTools(t, 4, 60)
	m.touchEnabled = true
	m.header = "test header"
	m.height = 24
	m.setStatus("browsing", rankHint)
	m.refresh()

	lm := m.computeLayout()
	beforeSelected := m.selectedItem
	beforeNav := m.navigationMode

	// Header row: y = 0
	if lm.HeaderRows > 0 {
		tap(m, 0)
		if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
			t.Fatal("tap on header changed state")
		}
	}

	// Status row (below viewport, above separator)
	statusY := lm.HeaderRows + lm.ViewportRows + lm.UnseenRows
	if lm.RuntimeStatusRows > 0 {
		tap(m, statusY)
		if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
			t.Fatal("tap on status row changed state")
		}
	}

	// Footer row: y = m.height - 1
	tap(m, m.height-1)
	if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
		t.Fatal("tap on footer changed state")
	}

	// Composer row: y = m.height - 2
	tap(m, m.height-2)
	if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
		t.Fatal("tap on composer changed state")
	}

	// Out of bounds taps
	tap(m, -1)
	tap(m, m.height+5)
	if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
		t.Fatal("out of bounds tap changed state")
	}
}

func TestTapBelowLastLineIsIgnored(t *testing.T) {
	m := feedWithTools(t, 1, 60)
	m.touchEnabled = true
	m.height = 24
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	lm := m.computeLayout()
	top := lm.viewportTop()
	contentRows := len(m.lines)
	emptyRow := top + contentRows + 2
	if emptyRow >= top+lm.ViewportRows {
		t.Fatal("viewport not tall enough for blank rows test")
	}

	beforeSelected := m.selectedItem
	beforeNav := m.navigationMode

	tap(m, emptyRow)

	if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
		t.Fatalf("tap on blank viewport padding changed state: selected=%d nav=%v", m.selectedItem, m.navigationMode)
	}
}

// TestShortContentPadsToBottom verifies that when the feed is shorter than
// the viewport AND following, blank rows are emitted BEFORE the content —
// not after. This is the structural property that eliminates the dead space
// between the last reply and the composer on small screens.
func TestShortContentPadsToBottom(t *testing.T) {
	m := feedWithTools(t, 1, 60)
	m.height = 24
	m.follow = true
	m.scrollTop = m.bottomStart(m.computeLayout().ViewportRows)
	m.refresh()

	lm := m.computeLayout()
	top := lm.viewportTop()
	topPad := m.viewportTopPadding(lm)

	rows := strings.Split(m.View(), "\n")
	// The first topPad rows inside the viewport must be blank.
	for i := top; i < top+topPad; i++ {
		if rows[i] != "" {
			t.Fatalf("row %d should be blank padding, got %q", i, rows[i])
		}
	}
	// The first feed line must appear at top+topPad, not at top.
	if top+topPad >= len(rows) {
		t.Fatalf("viewport top+padding %d exceeds view rows", top+topPad)
	}
	if rows[top+topPad] == "" {
		t.Fatalf("expected feed content at row %d, got blank", top+topPad)
	}
	if rows[top+topPad] != m.lines[0] {
		t.Fatalf("row %d = %q, want first feed line %q",
			top+topPad, rows[top+topPad], m.lines[0])
	}
	// No blank rows between the last feed line and the bottom separator.
	lastContent := top + topPad + len(m.lines) - 1
	if lastContent >= len(rows) {
		t.Fatalf("last content row %d exceeds view rows", lastContent)
	}
}

func TestPressWithoutReleaseSelectsNothing(t *testing.T) {
	m := feedWithTools(t, 4, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	y := rowOfCard(m, 1)
	if y < 0 {
		t.Fatal("card 1 not visible")
	}
	beforeSelected := m.selectedItem
	beforeNav := m.navigationMode

	_, cmd := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      1,
		Y:      y,
	})

	if cmd != nil {
		t.Fatal("press returned non-nil cmd")
	}
	if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
		t.Fatal("press without release changed selection or navigation mode")
	}
}

func TestTapPressReleaseSelectsCard(t *testing.T) {
	m := feedWithTools(t, 4, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	y := rowOfCard(m, 2)
	if y < 0 {
		t.Fatal("card 2 not visible")
	}

	// Clean press and release at identical coordinates
	_, cmd1 := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: y})
	if cmd1 != nil {
		t.Fatal("press returned non-nil cmd")
	}
	_, cmd2 := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 1, Y: y})
	if cmd2 != nil {
		t.Fatal("release returned non-nil cmd")
	}

	if m.selectedItem != 2 {
		t.Fatalf("selectedItem = %d, want 2", m.selectedItem)
	}
}

func TestDragReleaseDoesNotSelectCard(t *testing.T) {
	m := feedWithTools(t, 6, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	y1 := rowOfCard(m, 1)
	y2 := rowOfCard(m, 3)
	if y1 < 0 || y2 < 0 {
		t.Fatalf("cards not visible: y1=%d y2=%d", y1, y2)
	}

	beforeSelected := m.selectedItem
	beforeNav := m.navigationMode

	// Simulate drag sequence: Press on card 1, Motion to card 3, Release on card 3
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: y1})
	m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft, X: 1, Y: y2})
	_, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 1, Y: y2})

	if cmd != nil {
		t.Fatal("drag release returned non-nil cmd")
	}
	if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
		t.Fatalf("drag motion-release selected a card: selected=%d nav=%v", m.selectedItem, m.navigationMode)
	}

	// Also test Press at y1, Release at y2 directly without intermediate motion
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: y1})
	m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 1, Y: y2})

	if m.selectedItem != beforeSelected || m.navigationMode != beforeNav {
		t.Fatalf("coordinate-mismatch release selected a card: selected=%d nav=%v", m.selectedItem, m.navigationMode)
	}
}

func TestDragReleaseDoesNotToggleExpansion(t *testing.T) {
	m := feedWithTools(t, 4, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.enterNavigation()
	m.selectItem(1)
	m.refresh()

	items := m.navigationItems()
	cardID := items[1].ID
	y1 := rowOfCard(m, 1)
	if y1 < 0 {
		t.Fatal("card 1 not visible")
	}

	// Drag horizontally across card 1 (text selection gesture in terminal)
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: y1})
	m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft, X: 15, Y: y1})
	_, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 15, Y: y1})

	if cmd != nil {
		t.Fatal("horizontal drag release returned non-nil cmd")
	}
	if m.effectiveExpanded(cardID) {
		t.Fatal("horizontal drag release toggled card expansion")
	}
}

func TestPointerNeverAnswersPermission(t *testing.T) {
	m := feedWithPendingPermission(t, 60) // يبني مودالًا معلّقًا
	before := m.selectedItem
	for y := 0; y < m.height; y++ {
		_, cmd1 := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: y})
		if cmd1 != nil {
			msg := cmd1()
			t.Fatalf("pointer press during modal returned non-nil cmd producing %T", msg)
		}
		_, cmd2 := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 1, Y: y})
		if cmd2 != nil {
			msg := cmd2()
			t.Fatalf("pointer release during modal returned non-nil cmd producing %T", msg)
		}
	}
	if m.decisionPending {
		t.Fatal("a tap submitted a permission decision")
	}
	if !m.modalVisible {
		t.Fatal("a tap dismissed the permission modal")
	}
	if m.selectedItem != before {
		t.Fatal("a tap changed selection while the modal owned input")
	}
}

func TestPointerNeverExecutesATool(t *testing.T) {
	m := feedWithTools(t, 5, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.running = false
	m.busy = false
	m.runningTool = ""
	m.refresh()

	for y := 0; y < m.height; y++ {
		_, cmd1 := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 1, Y: y})
		if cmd1 != nil {
			msg := cmd1()
			t.Fatalf("pointer press at y=%d returned non-nil cmd producing %T", y, msg)
		}
		_, cmd2 := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 1, Y: y})
		if cmd2 != nil {
			msg := cmd2()
			t.Fatalf("pointer release at y=%d returned non-nil cmd producing %T", y, msg)
		}
	}

	if m.running || m.busy || m.runningTool != "" {
		t.Fatal("pointer tap triggered tool execution or set running/busy")
	}
}

func TestTapLeavesProjectionUnchanged(t *testing.T) {
	m := feedWithTools(t, 5, 60)
	m.touchEnabled = true
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	itemsBefore := len(m.proj.Items())
	noticesBefore := len(m.notices)

	for y := 0; y < m.height; y++ {
		tap(m, y)
	}

	if len(m.proj.Items()) != itemsBefore {
		t.Fatalf("projector items count changed: before=%d after=%d", itemsBefore, len(m.proj.Items()))
	}
	if len(m.notices) != noticesBefore {
		t.Fatalf("notices count changed: before=%d after=%d", noticesBefore, len(m.notices))
	}
}

func TestPointerIgnoredWhenTouchDisabled(t *testing.T) {
	m := feedWithTools(t, 4, 60)
	m.SetTouch(false)
	m.height = 20
	m.follow = false
	m.scrollTop = 0
	m.refresh()

	y := rowOfCard(m, 1)
	if y < 0 {
		t.Fatal("card 1 not visible")
	}
	tap(m, y)

	if m.navigationMode || m.selectedItem != -1 {
		t.Fatal("tap took effect while touch was disabled")
	}
}

func TestTapAtEveryWidthSelectsConsistently(t *testing.T) {
	widths := []int{20, 30, 40, 60, 80, 120}
	for _, w := range widths {
		t.Run(fmt.Sprintf("width_%d", w), func(t *testing.T) {
			m := feedWithTools(t, 4, w)
			m.touchEnabled = true
			m.height = 20
			m.follow = false
			m.scrollTop = 0
			m.refresh()

			y := rowOfCard(m, 1)
			if y < 0 {
				t.Fatalf("card 1 not visible at width %d", w)
			}
			tap(m, y)
			if m.selectedItem != 1 {
				t.Fatalf("at width %d: selectedItem = %d, want 1", w, m.selectedItem)
			}
		})
	}
}
