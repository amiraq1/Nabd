package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"
)

// TestRenderGlobResultNotCollapsed asserts at the render layer (not via
// glob.Run) that a short listing survives whole and a long paginated one
// still carries its continuation tail plus a result row above it.
//
// This is the runtime proof of the "glob * → one line" defect: under the old
// one-line tail, only the bottom row survived — for a 5-row listing that
// dropped 4 files, and for a "50 of 166" listing that dropped the 50 names
// and kept only the summary. The result set itself vanished.
func TestRenderGlobResultNotCollapsed(t *testing.T) {
	// Short list (5 hits): every row must render, not just the last.
	ev := agent.Event{Type: agent.ToolEnd, Call: &agent.ToolCall{
		ID: "g1", Name: "glob", OK: true, Output: "a.go\nb.go\nc.go\nd.go\ne.go\n",
	}}
	out := RenderEvent(ev, DefaultWidth)
	for _, f := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		if !strings.Contains(out, f) {
			t.Errorf("short glob listing must render all rows, missing %q:\n%s", f, out)
		}
	}

	// Long paginated list: the offset= tail must survive AND a result row
	// just above it must survive — the model needs both to page forward.
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		b.WriteString(fmt.Sprintf("file_%02d.go\n", i))
	}
	b.WriteString("showing 1-50 of 166 · continue with offset=51\n")
	ev2 := agent.Event{Type: agent.ToolEnd, Call: &agent.ToolCall{
		ID: "g2", Name: "glob", OK: true, Output: b.String(),
	}}
	out2 := RenderEvent(ev2, DefaultWidth)
	if !strings.Contains(out2, "continue with offset=51") {
		t.Errorf("paginated glob must expose a continuation affordance, got:\n%s", out2)
	}
	if !strings.Contains(out2, "file_50.go") {
		t.Errorf("render must keep a result row above the pagination tail, got:\n%s", out2)
	}
}
