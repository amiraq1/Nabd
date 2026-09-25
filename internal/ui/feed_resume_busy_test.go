package ui

import (
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFeedCancelAfterResumeReturnsToIdleWithinDeadline: rebuilding a resumed
// session replays history, not liveness. A historical ToolStart must not leave
// the feed claiming a run is in flight, and Ctrl+C on such a replay must not
// park the transient row in "canceling…" — no runner exists here, so no
// doneMsg can ever arrive to clear it.
func TestFeedCancelAfterResumeReturnsToIdleWithinDeadline(t *testing.T) {
	f := NewFeed()
	f.BuildFromEvents([]agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "nabd test"},
		{Seq: 2, Type: agent.UserMsg, Text: "q"},
		{Seq: 3, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
		// Deliberately no ToolEnd and no TurnEnd: the previous process died
		// mid-tool, which is exactly the resume case that hung.
		{Seq: 4, Type: agent.RunEnd, Text: "session ended"},
	})

	inherited := f.running || f.busy
	footer := f.footerText(80)
	advertisesCancel := strings.Contains(footer, "^C cancel")

	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	// The ladder may quit (idle + empty composer). What must never survive is a
	// canceling state, because nothing can ever complete it here.
	settled := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.status != "canceling…" && !f.running && !f.busy {
			settled = true
			break
		}
		time.Sleep(pollBackoff)
		_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	}

	if inherited || advertisesCancel || !settled {
		t.Fatalf("resumed feed must start idle and stay recoverable: "+
			"inherited running/busy=%v footer=%q status=%q running=%v busy=%v settled=%v",
			inherited, footer, f.status, f.running, f.busy, settled)
	}
}

// TestFeedLiveRunStaysBusyWhileToolRunsAndIdlesAfterDoneMsg is the guard for
// the behavior the resume fix must not disturb: a run started from the
// composer stays busy while its tool executes, ToolEnd alone does not open the
// send gate, and doneMsg is what returns the feed to idle.
func TestFeedLiveRunStaysBusyWhileToolRunsAndIdlesAfterDoneMsg(t *testing.T) {
	f, _ := feedWithRunner(t)

	if cmd := sendAndRun(f, "do work"); cmd == nil {
		t.Fatal("send must produce a run command")
	}
	if !f.running || !f.busy {
		t.Fatalf("after send: running=%v busy=%v, want both true", f.running, f.busy)
	}

	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
	}})
	if !f.busy || f.runningTool != "bash" {
		t.Fatalf("during tool run: busy=%v runningTool=%q, want busy with bash", f.busy, f.runningTool)
	}

	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
	}})
	if !f.busy {
		t.Fatal("ToolEnd opened the send gate; only doneMsg may end the run")
	}
	if f.runningTool != "" {
		t.Fatalf("runningTool = %q after ToolEnd, want empty", f.runningTool)
	}

	f.Update(doneMsg{})
	if f.running || f.busy || f.runningTool != "" || f.status != "" {
		t.Fatalf("after doneMsg: running=%v busy=%v runningTool=%q status=%q, want fully idle",
			f.running, f.busy, f.runningTool, f.status)
	}
}
