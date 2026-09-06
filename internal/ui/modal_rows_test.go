package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"nabd/internal/agent"
)

// The permission modal must render exactly the rows the layout reserved for
// it: computeLayout asks lineCount(avail) and View then calls
// view(w, lm.ModalRows). Before permModalShape those were two independent
// ladders and they disagreed — with args present and no decision pending,
// lineCount(9) promised 9 rows while view emitted 8 (Args row included,
// blank rows dropped), leaving a stray blank row inside the chrome.
func TestPermModalRowsMatchLineCount(t *testing.T) {
	cases := []struct {
		name            string
		args            string
		selected        int
		decisionPending bool
	}{
		{name: "with args, first choice selected", args: `{"path":"main.go"}`, selected: 0},
		// selected=-1 is no longer a valid default (now Deny), but the modal must
		// still render cleanly for any out-of-range index: row count is driven by
		// shape(), not by selected, so this stays a guard for that property.
		{name: "with args, invalid selection falls back to Deny", args: `{"cmd":"ls -la"}`, selected: -1},
		{name: "no args", args: "", selected: 1},
		{name: "empty object args counts as no args", args: "{}", selected: 2},
		{name: "decision pending with args", args: `{"path":"main.go"}`, selected: 2, decisionPending: true},
		{name: "decision pending without args", args: "", selected: 0, decisionPending: true},
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
				t.Fatalf("full height %d is below the 3-row modal minimum", full)
			}

			for _, w := range []int{20, 40, 80} {
				// n runs past full on purpose: the layout may offer more rows
				// than the modal needs.
				for n := 1; n <= full+2; n++ {
					reserved := m.lineCount(n)
					rendered := len(strings.Split(m.view(w, n), "\n"))
					if rendered != reserved {
						t.Fatalf("width=%d maxRows=%d: reserved %d rows, rendered %d",
							w, n, reserved, rendered)
					}
					// The modal never overflows the space it was given, except
					// for the documented 3-row floor: below that it stays at 3
					// and computeLayout clamps avail to 3 for the same reason.
					if n >= 3 && reserved > n {
						t.Fatalf("width=%d maxRows=%d: reserved %d rows, more than allowed",
							w, n, reserved)
					}
					if reserved < 3 {
						t.Fatalf("width=%d maxRows=%d: reserved %d rows, below the 3-row minimum",
							w, n, reserved)
					}
					// Idempotence: View passes the resolved height back in, so
					// resolving it again must not shrink the modal further.
					if again := m.lineCount(reserved); again != reserved {
						t.Fatalf("width=%d maxRows=%d: lineCount(%d) = %d, not idempotent",
							w, n, reserved, again)
					}
				}
			}
		})
	}
}

// A closed modal occupies no rows and renders nothing, so the layout never
// reserves chrome for it.
func TestPermModalClosedTakesNoRows(t *testing.T) {
	m := newPermissionModal()
	if got := m.lineCount(); got != 0 {
		t.Fatalf("closed modal reserved %d rows, want 0", got)
	}
	if got := m.lineCount(10); got != 0 {
		t.Fatalf("closed modal reserved %d rows with a limit, want 0", got)
	}
	if got := m.view(40, 10); got != "" {
		t.Fatalf("closed modal rendered %q, want empty", got)
	}

	m.open(&agent.ToolCall{ID: "call_1", Name: "write_file"})
	m.close()
	if got := m.lineCount(); got != 0 {
		t.Fatalf("reopened-then-closed modal reserved %d rows, want 0", got)
	}
	if got := m.view(40); got != "" {
		t.Fatalf("reopened-then-closed modal rendered %q, want empty", got)
	}
}
