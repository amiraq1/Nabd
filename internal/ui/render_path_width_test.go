package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderPathRowIsNotWrapped is a characterisation test for the debt entry
// EDIT_READ_ROW_NOT_WRAPPED in docs/TECH_DEBT.md.
//
// The ✎ edit row and the ✂ truncated-read row return dim.Render(s) and
// warn.Render(...) without consulting the width argument, so a long path is
// clipped by the terminal rather than by nabd — unlike the tool-call and
// permission-prompt rows, which pass through block(...) and wrap(...). This
// test pins the current, defective behaviour on purpose.
//
// Closure: when the rows are wrapped, replace this test with one asserting the
// row fits the width and close the debt entry. Do not weaken it silently.
func TestRenderPathRowIsNotWrapped(t *testing.T) {
	pinPlainRender(t)

	long := "src/" + strings.Repeat("a-very-long-directory-name/", 6) + "file.go"
	if len(long) <= 40 {
		t.Fatalf("fixture path is %d bytes, want it to exceed the 40-column width", len(long))
	}

	got := editRow(long, 40)
	if !strings.Contains(got, long) {
		t.Errorf("edit row = %q, want the unwrapped path pinned by TECH_DEBT", got)
	}
	if w := ansi.StringWidth(got); w <= 40 {
		t.Errorf("edit row width = %d, want > 40: the debt entry claims the row is not wrapped", w)
	}

	readGot := truncatedReadRow(long, 40)
	if !strings.Contains(readGot, long) {
		t.Errorf("read row = %q, want the unwrapped path pinned by TECH_DEBT", readGot)
	}
}
