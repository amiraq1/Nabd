package ui

import (
	"strings"
	"testing"
)

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
	if top >= len(rows) {
		t.Fatalf("viewportTop %d outside the rendered view (%d rows)", top, len(rows))
	}
	want := m.lines[m.scrollTop]
	if got := rows[top]; got != want {
		t.Fatalf("row %d is %q, but scrollTop line is %q", top, got, want)
	}
}
