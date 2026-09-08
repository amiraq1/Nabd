package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"

	"github.com/charmbracelet/x/ansi"
)

// menuItems builds a deterministic set of n slash menu items for accounting
// tests. The counts 1/3/12 exercise the menu's row reservation at the small
// terminal heights where the degradation ladder compresses the menu to its
// 2-row floor.
func menuItems(n int) []SlashCommand {
	out := make([]SlashCommand, n)
	for i := range out {
		out[i] = SlashCommand{
			Usage:       fmt.Sprintf("/cmd%d", i),
			Description: fmt.Sprintf("command number %d", i),
		}
	}
	return out
}

// menuReserveAndDrawn captures the contract under test: the rows computeLayout
// reserves for the slash menu (lm.MenuRows) must equal the rows menu.view
// actually draws for that reservation.
func menuReserveAndDrawn(f *Feed) (reserved, drawn int) {
	lm := f.computeLayout()
	if lm.MenuRows == 0 {
		return 0, 0
	}
	viewed := f.menu.view(lm.TerminalWidth, lm.MenuRows)
	return lm.MenuRows, visualRowsOf(viewed, lm.TerminalWidth)
}

// TestLayoutContract is the green baseline: in a non-degenerate frame the
// menu's reservation matches its rendering, output is valid UTF-8, and the
// row-counting helpers agree with each other. TestMenuRowAccounting violates
// the first clause once the menu is squeezed to its floor.
func TestLayoutContract(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	f.menu.open(menuItems(3))
	lm := f.computeLayout()
	if lm.MenuRows != 5 {
		t.Fatalf("expected menu reserved 5 rows, got %d", lm.MenuRows)
	}
	reserved, drawn := menuReserveAndDrawn(f)
	if drawn != reserved {
		t.Fatalf("layout contract: menu reserved %d rows, drawn %d", reserved, drawn)
	}
	if !isValidUTF8(f.menu.view(lm.TerminalWidth, lm.MenuRows)) {
		t.Fatalf("menu view is not valid UTF-8")
	}

	wide := strings.Repeat("a", 100)
	if got := len(constrainLines(wide, 10)); got != 10 {
		t.Errorf("constrainLines: got %d lines, want 10", got)
	}
	for _, l := range strings.Split(constrainWidth(wide, 10), "\n") {
		if ansi.StringWidth(l) > 10 {
			t.Errorf("constrainWidth: line %q is %d cells > 10", l, ansi.StringWidth(l))
		}
	}
	if w := ansi.StringWidth(asciiSeparatorLine(10)); w != 10 {
		t.Errorf("asciiSeparatorLine: %d cells, want 10", w)
	}
}

// TestMenuRowAccounting verifies that the slash menu's reserved rows
// (computeLayout -> lm.MenuRows) match the rows menu.view draws. At small
// terminal heights the degradation ladder compresses the menu to its 2-row
// floor: lineCount() returns 2, but menu.view() draws 3 (header + 1 item +
// footer) because maxItemRows is clamped to a minimum of 1. That
// reservation != rendering mismatch is the documented defect in layout.go /
// slash_menu.go.
func TestMenuRowAccounting(t *testing.T) {
	cases := []struct {
		name  string
		items []SlashCommand
		modal bool
	}{
		{"menu_1", menuItems(1), false},
		{"menu_3", menuItems(3), false},
		{"menu_12", menuItems(12), false},
		{"modal_and_menu", menuItems(3), true},
	}
	for _, tc := range cases {
		for h := 2; h <= 10; h++ {
			tag := fmt.Sprintf("%s/h=%d", tc.name, h)
			t.Run(tag, func(t *testing.T) {
				f := newFeedAt(t, 80, h)
				f.menu.open(tc.items)
				if tc.modal {
					f.permModal.open(&agent.ToolCall{ID: "m1", Name: "bash"})
					f.modalVisible = true
				}
				reserved, drawn := menuReserveAndDrawn(f)
				if reserved == 0 {
					t.Skipf("menu not reserved at h=%d", h)
				}
				if drawn != reserved {
					t.Errorf("menu row accounting: reserved %d rows, drawn %d rows (%d items, modal=%v)",
						reserved, drawn, len(tc.items), tc.modal)
				}
			})
		}
	}
}

// TestModalRowAccounting mirrors the menu contract for the permission modal.
// The modal resolves its shape through a single shape() ladder shared by
// lineCount and view, so the reservation always matches the rendering across
// the full height spectrum.
func TestModalRowAccounting(t *testing.T) {
	cases := []struct {
		name            string
		args            string
		selected        int
		decisionPending bool
	}{
		{"with args, first choice selected", `{"path":"main.go"}`, 0, false},
		{"with args, invalid selection falls back to Deny", `{"cmd":"ls -la"}`, -1, false},
		{"no args", "", 1, false},
		{"empty object args counts as no args", "{}", 2, false},
		{"decision pending with args", `{"path":"main.go"}`, 2, true},
		{"decision pending without args", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newPermissionModal()
			call := &agent.ToolCall{ID: "call_1", Name: "write_file"}
			if tc.args != "" {
				call.Args = json.RawMessage(tc.args)
			}
			m.open(call)
			m.selected = tc.selected
			m.decisionPending = tc.decisionPending

			full := m.lineCount()
			if full < 3 {
				t.Fatalf("full height %d below 3-row modal minimum", full)
			}
			for _, w := range []int{20, 40, 80} {
				for n := 1; n <= full+2; n++ {
					reserved := m.lineCount(n)
					viewed := m.view(w, n)
					drawn := visualRowsOf(viewed, w)
					if !isValidUTF8(viewed) {
						t.Fatalf("width=%d n=%d: modal view not valid UTF-8", w, n)
					}
					if drawn != reserved {
						t.Errorf("width=%d n=%d: modal reserved %d rows, drawn %d", w, n, reserved, drawn)
					}
				}
			}
		})
	}
}

// TestFrameHeightExact asserts the rendered frame fills the terminal to
// exactly h rows. The defensive clamp in View() trims overflow (including the
// menu's 2->3 row overdraw) from the top, so the frame height stays exact
// even when an individual chrome element overdraws its reservation.
func TestFrameHeightExact(t *testing.T) {
	cases := []struct {
		name  string
		items []SlashCommand
		modal bool
	}{
		{"idle", nil, false},
		{"menu_3", menuItems(3), false},
		{"modal_and_menu", menuItems(3), true},
	}
	for _, tc := range cases {
		for h := 2; h <= 12; h++ {
			tag := fmt.Sprintf("%s/h=%d", tc.name, h)
			t.Run(tag, func(t *testing.T) {
				f := newFeedAt(t, 80, h)
				if tc.items != nil {
					f.menu.open(tc.items)
				}
				if tc.modal {
					f.permModal.open(&agent.ToolCall{ID: "m1", Name: "bash"})
					f.modalVisible = true
				}
				v := f.View()
				if got := visualRowsOf(v, 80); got != h {
					t.Errorf("frame height: visualRowsOf=%d, terminal height=%d\n%s", got, h, v)
				}
			})
		}
	}
}

// TestBottomAnchor asserts the footer stays glued to the bottom row of the
// terminal in every size, because View() emits the composer+footer block
// last and the clamp keeps the bottom m.height rows intact.
func TestBottomAnchor(t *testing.T) {
	for _, sz := range allTermSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)
			v := f.View()
			if got := visualRowsOf(v, sz.width); got != sz.height {
				t.Fatalf("frame not bottom-anchored to height %d (got %d)", sz.height, got)
			}
			lines := strings.Split(v, "\n")
			footer := lines[len(lines)-1]
			if strings.TrimSpace(footer) == "" {
				t.Fatalf("footer is blank at the bottom row:\n%s", v)
			}
		})
	}
}
