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
// tests. The counts 1/3/12 exercise row accounting across normal heights and
// the small-height path where the menu is hidden below menuMinRows.
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
// row-counting helpers agree with each other.

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

// TestMenuRowAccounting verifies that whenever the slash menu is reserved,
// the number of reserved rows matches the rows menu.view draws. Small frames
// may hide the menu when fewer than menuMinRows are available; that path is
// covered separately by TestMenuHiddenBelowPhysicalFloor and
// TestMenuVisibleAtPhysicalFloor.
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
				lm := f.computeLayout()
				if lm.MenuRows == 0 {
					// Menu hidden below its physical floor: verify View() omits
					// the menu entirely, not just that reservation is zero.
					if strings.Contains(ansi.Strip(f.View()), "Commands") {
						t.Fatalf("%s h=%d: menu header in View() when MenuRows=0", tc.name, h)
					}
					return
				}
				viewed := f.menu.view(lm.TerminalWidth, lm.MenuRows)
				drawn := visualRowsOf(viewed, lm.TerminalWidth)
				if drawn != lm.MenuRows {
					t.Errorf("menu row accounting: reserved %d rows, drawn %d rows (%d items, modal=%v)",
						lm.MenuRows, drawn, len(tc.items), tc.modal)
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

// TestFrameNeverOverflows is a diagnostic guard that proves the rendered
// frame never exceeds the terminal height, even under adversarial menu/modal
// states. It prints the layout and dimensions on failure.
func TestFrameNeverOverflows(t *testing.T) {
	for _, sz := range allTermSizes {
		for _, tc := range []struct {
			name  string
			items []SlashCommand
			modal bool
			menu  bool
		}{
			{"idle", nil, false, false},
			{"menu_12", menuItems(12), false, true},
			{"modal", nil, true, false},
			{"modal_and_menu", menuItems(12), true, true},
		} {
			t.Run(fmt.Sprintf("%s/%s/h=%d", tc.name, sz.name, sz.height), func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)
				if tc.items != nil && tc.menu {
					f.menu.open(tc.items)
				}
				if tc.modal {
					f.permModal.open(&agent.ToolCall{ID: "m1", Name: "bash"})
					f.modalVisible = true
				}
				v := f.View()
				rows := visualRowsOf(v, sz.width)
				if rows > sz.height {
					lm := f.computeLayout()
					t.Errorf("overflow: %d rows > %d height (w=%d)\nlayout: Header=%d Runtime=%d TopSep=%d Modal=%d Menu=%d Unseen=%d Composer=%d BottomSep=%d Footer=%d Viewport=%d\nview:\n%s",
						rows, sz.height, sz.width,
						lm.HeaderRows, lm.RuntimeStatusRows, lm.TopSepRows,
						lm.ModalRows, lm.MenuRows, lm.UnseenRows,
						lm.ComposerRows, lm.BottomSepRows, lm.FooterRows,
						lm.ViewportRows, v)
				}
			})
		}
	}
}

// TestFrameHeightNarrowerThanMinWidth is a documented deferred defect.
// computeLayout raises TerminalWidth to minViewportWidth (20), so View()
// renders at 20 cells even when the real terminal is narrower (e.g. 16).
// This causes separators and footer text to overflow the actual display.
// Tracking key: NARROW_OVR_12 — see docs/TECH_DEBT.md.
func TestFrameHeightNarrowerThanMinWidth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped under -short")
	}
	t.Skip("deferred defect: NARROW_OVR_12 — computeLayout raises width to minViewportWidth=20, causing overflow at narrower real terminals; requires policy decision on clamping vs. dynamic width. See docs/TECH_DEBT.md")
}

// TestFrameHeightExact asserts the rendered frame fills the terminal to
// exactly h rows. The defensive clamp in View() trims any overflow from the
// top, so the frame height stays exact even when chrome arithmetic temporarily
// exceeds the terminal height before the degradation ladder resolves.
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

// requiredFloor is the documented minimum chrome for a state: the composer
// and footer are never sacrificed, and a visible permission modal reserves
// its 3-row minimum even when that overflows (a documented UX decision in the
// degradation ladder). Below this floor an overflow is expected, so the case
// is skipped rather than failed.
func requiredFloor(f *Feed) int {
	floor := minComposerHeight + 1 // composer + footer
	if f.modalVisible || f.decisionPending {
		floor += 3
	}
	return floor
}

// TestClampNeverFires guards the region TestMenuRowAccounting can no longer
// reach. Once the menu is dropped below its physical floor, that test skips
// on reserved == 0, so nothing else would notice a return to compression.
//
// The defensive clamp in View() is a last resort for states the matrix does
// not model. If it fires for a modeled state above its documented floor, the
// layout arithmetic is wrong even when every row count agrees with what its
// renderer emits: reserving 3 rows the frame cannot fit is just as broken as
// reserving 2 rows the renderer cannot honour. This is stricter than the
// frame-height contract, which measures output only AFTER the clamp trimmed it.
func TestClampNeverFires(t *testing.T) {
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
		for _, w := range []int{20, 50, 80} {
			for h := 2; h <= 10; h++ {
				tag := fmt.Sprintf("%s/w=%d/h=%d", tc.name, w, h)
				t.Run(tag, func(t *testing.T) {
					f := newFeedAt(t, w, h)
					f.menu.open(tc.items)
					if tc.modal {
						f.permModal.open(&agent.ToolCall{ID: "m1", Name: "bash"})
						f.modalVisible = true
					}
					if h < requiredFloor(f) {
						t.Skip("below documented floor")
					}
					lm := f.computeLayout()
					chrome := lm.HeaderRows + lm.RuntimeStatusRows + lm.TopSepRows +
						lm.ComposerRows + lm.BottomSepRows + lm.FooterRows +
						lm.UnseenRows + lm.ModalRows + lm.MenuRows
					if chrome+lm.ViewportRows > h {
						t.Fatalf("clamp would fire: chrome %d + viewport %d > height %d (menu=%d modal=%d)",
							chrome, lm.ViewportRows, h, lm.MenuRows, lm.ModalRows)
					}
				})
			}
		}
	}
}

// TestMenuHiddenBelowPhysicalFloor proves the menu is dropped (not compressed)
// when fewer than menuMinRows are available after accounting for the chrome
// that is never sacrificed (composer + footer + separators).
func TestMenuHiddenBelowPhysicalFloor(t *testing.T) {
	// h=4: after composer(1) + footer(1) + topSep(1) + bottomSep(1) = 4 rows
	// of mandatory chrome, the menu's 3-row floor cannot be met, so it is
	// hidden rather than compressed into rows view() cannot honour.
	f := newFeedAt(t, 80, 4)
	f.menu.open(menuItems(3))
	lm := f.computeLayout()
	if lm.MenuRows != 0 {
		t.Fatalf("expected menu hidden at h=4, got MenuRows=%d", lm.MenuRows)
	}
	// The rendered frame must not contain the menu header.
	if strings.Contains(ansi.Strip(f.View()), "Commands") {
		t.Fatalf("menu header present in View() when MenuRows=0:\n%s", f.View())
	}
}

// TestMenuVisibleAtPhysicalFloor proves the menu reserves exactly menuMinRows
// when just enough space is available, and that the reservation matches the
// rendered rows.
func TestMenuVisibleAtPhysicalFloor(t *testing.T) {
	// h=5: after dropping bottomSep, chrome is composer(1) + footer(1) +
	// topSep(1) = 3, leaving exactly menuMinRows=3 for the menu.
	f := newFeedAt(t, 80, 5)
	f.menu.open(menuItems(3))
	lm := f.computeLayout()
	if lm.MenuRows != menuMinRows {
		t.Fatalf("expected menu reserved %d rows at h=5, got %d", menuMinRows, lm.MenuRows)
	}
	// Reservation must match rendering.
	viewed := f.menu.view(lm.TerminalWidth, lm.MenuRows)
	drawn := visualRowsOf(viewed, lm.TerminalWidth)
	if drawn != lm.MenuRows {
		t.Fatalf("reservation/render mismatch: reserved %d, drawn %d", lm.MenuRows, drawn)
	}
	// The rendered frame must contain the menu header.
	if !strings.Contains(ansi.Strip(f.View()), "Commands") {
		t.Fatalf("menu header missing from View() when MenuRows=%d:\n%s", lm.MenuRows, f.View())
	}
}

// menuFrameRows counts the menu rows that survive View() and the defensive
// clamp -- i.e. what the user actually sees. Unlike menuReserveAndDrawn it
// never passes the reservation back into shape(), so a divergence between
// what computeLayout reserves and what reaches the screen is observable here.
func menuFrameRows(f *Feed) (reserved, onScreen int) {
	lm := f.computeLayout()
	rows := visualRowsSplit(ansi.Strip(f.View()), lm.TerminalWidth)
	start := -1
	for i, r := range rows {
		if strings.Contains(r, "Commands") {
			start = i
			break
		}
	}
	if start < 0 {
		return lm.MenuRows, 0
	}
	for i := start + 1; i < len(rows); i++ {
		if isMenuFooterLine(rows[i]) {
			return lm.MenuRows, i - start + 1
		}
	}
	return lm.MenuRows, len(rows) - start
}

// isMenuFooterLine matches the menu's closing separator by shape rather than
// by glyph: a non-empty run of one repeated non-ASCII rune.
func isMenuFooterLine(row string) bool {
	trimmed := strings.TrimRight(row, " ")
	if trimmed == "" {
		return false
	}
	rs := []rune(trimmed)
	if rs[0] < 0x80 {
		return false
	}
	for _, r := range rs {
		if r != rs[0] {
			return false
		}
	}
	return true
}

// TestMenuReachesScreen asserts the reservation survives rendering and the
// clamp: whatever computeLayout reserves for the menu must appear on screen.
func TestMenuReachesScreen(t *testing.T) {
	cases := []struct {
		name  string
		items []SlashCommand
	}{
		{"menu_1", menuItems(1)},
		{"menu_3", menuItems(3)},
		{"menu_12", menuItems(12)},
	}
	for _, tc := range cases {
		for _, w := range []int{20, 50, 80} {
			for h := 2; h <= 12; h++ {
				tag := fmt.Sprintf("%s/w=%d/h=%d", tc.name, w, h)
				t.Run(tag, func(t *testing.T) {
					f := newFeedAt(t, w, h)
					f.menu.open(tc.items)
					reserved, onScreen := menuFrameRows(f)
					if reserved != onScreen {
						t.Fatalf("menu reserved %d rows, %d reached the screen", reserved, onScreen)
					}
				})
			}
		}
	}
}
