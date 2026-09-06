package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// stripANSI removes ANSI escape codes from s for stable snapshot assertions.
func stripANSI(s string) string {
	return ansi.Strip(s)
}

// TestSpinnerStopsWhenRuntimeReturnsReady verifies that when a run finishes
// and the runtime returns to the Ready / idle state:
//   - f.running and f.busy are false
//   - f.runningTool is cleared
//   - runtimeStatusText() is empty (no spinner needed)
//   - doneMsg returns no further tea.Cmd (no background ticks left running)
func TestSpinnerStopsWhenRuntimeReturnsReady(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Start a run: tool starts
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash", Args: json.RawMessage(`"pwd"`)}},
	}})

	// While tool is active, status reports it
	st := f.runtimeStatusText()
	if !strings.Contains(st, "Running bash") {
		t.Errorf("runtimeStatusText() = %q, want 'Running bash…'", st)
	}

	// Tool completes, then run finishes via doneMsg
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash", Output: "/home/termux\n", OK: true}},
		{Seq: 3, Type: agent.TurnEnd},
	}})

	_, cmd := f.Update(doneMsg{err: nil})

	// Must be idle / ready
	if f.running {
		t.Errorf("running should be false after doneMsg")
	}
	if f.busy {
		t.Errorf("busy should be false after doneMsg")
	}
	if f.runningTool != "" {
		t.Errorf("runningTool should be empty after doneMsg, got %q", f.runningTool)
	}
	if f.runtimeStatusText() != "" {
		t.Errorf("runtimeStatusText() should be empty (Ready) after clean doneMsg, got %q", f.runtimeStatusText())
	}
	if cmd != nil {
		t.Errorf("doneMsg must return nil cmd (no dangling background ticks), got %v", cmd)
	}
}

// TestLayoutSnapshotIdle verifies the two horizontal separators and composer panel
// structure in the idle state at 80x24.
func TestLayoutSnapshotIdle(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.SetHeader("nabd dev · session")
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	v := stripANSI(f.View())
	lines := strings.Split(v, "\n")

	// Verify header is the first line
	if !strings.Contains(lines[0], "nabd dev · session") {
		t.Errorf("first line should be header, got: %q", lines[0])
	}

	// Verify horizontal separators exist
	topSepIdx := -1
	bottomSepIdx := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "────") || strings.HasPrefix(l, "----") {
			if topSepIdx == -1 {
				topSepIdx = i
			} else if bottomSepIdx == -1 {
				bottomSepIdx = i
			}
		}
	}

	if topSepIdx == -1 {
		t.Fatalf("top separator not found in view:\n%s", v)
	}
	if bottomSepIdx == -1 {
		t.Fatalf("bottom separator not found in view:\n%s", v)
	}
	if bottomSepIdx <= topSepIdx {
		t.Fatalf("bottom separator (line %d) must come after top separator (line %d)", bottomSepIdx, topSepIdx)
	}

	// Between top and bottom separator should be the composer
	composerArea := strings.Join(lines[topSepIdx+1:bottomSepIdx], "\n")
	if !strings.Contains(composerArea, "›") && !strings.Contains(composerArea, ">") {
		t.Errorf("composer prompt not found between separators:\n%s", composerArea)
	}

	// Below bottom separator should be footer
	footerArea := strings.Join(lines[bottomSepIdx+1:], "\n")
	if !strings.Contains(footerArea, "Enter") {
		t.Errorf("footer shortcuts not found below bottom separator:\n%s", footerArea)
	}
}

// TestLayoutSnapshotToolRunning verifies that Runtime Status is shown above the
// top separator during tool execution.
func TestLayoutSnapshotToolRunning(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "read_file", Args: json.RawMessage(`"main.go"`)}},
	}})

	v := stripANSI(f.View())
	if !strings.Contains(v, "Running read_file") {
		t.Errorf("Runtime status 'Running read_file' must be in view:\n%s", v)
	}

	lines := strings.Split(v, "\n")
	statusIdx := -1
	topSepIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "Running read_file") {
			statusIdx = i
		}
		if (strings.HasPrefix(l, "────") || strings.HasPrefix(l, "----")) && topSepIdx == -1 {
			topSepIdx = i
		}
	}

	if statusIdx == -1 || topSepIdx == -1 || statusIdx >= topSepIdx {
		t.Errorf("status (line %d) must appear ABOVE top separator (line %d):\n%s", statusIdx, topSepIdx, v)
	}
}

// TestLayoutSnapshotPermissionModal verifies the modal card, paused composer,
// and horizontal borders in 40x20 mobile view.
func TestLayoutSnapshotPermissionModal(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.Update(tea.WindowSizeMsg{Width: 40, Height: 20})

	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`"rm -rf /tmp"`)}},
	}})

	v := stripANSI(f.View())

	// 1. Boxed card border
	if !strings.Contains(v, "+-- Permission") {
		t.Errorf("modal border missing in mobile view:\n%s", v)
	}
	// 2. Selection indicator
	if !strings.Contains(v, "[*]") && !strings.Contains(v, "[ ]") {
		t.Errorf("modal choices missing in mobile view:\n%s", v)
	}
	// 3. Paused composer line
	if !strings.Contains(v, "composer paused") {
		t.Errorf("paused composer line missing in mobile view:\n%s", v)
	}
	// 4. Invariant: never exceeds 20 rows
	lines := strings.Split(v, "\n")
	if len(lines) > 20 {
		t.Errorf("view has %d lines, exceeding terminal height 20:\n%s", len(lines), v)
	}
}

// TestLayoutSnapshotSlashMenuDockedAboveComposer verifies slash menu is positioned
// directly above the top separator of the composer panel.
func TestLayoutSnapshotSlashMenuDockedAboveComposer(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	v := stripANSI(f.View())
	lines := strings.Split(v, "\n")

	menuIdx := -1
	topSepIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "Commands") {
			menuIdx = i
		}
		if (strings.HasPrefix(l, "────") || strings.HasPrefix(l, "----")) && topSepIdx == -1 && i > menuIdx {
			topSepIdx = i
		}
	}

	if menuIdx == -1 {
		t.Fatalf("slash menu not found in view:\n%s", v)
	}
	if topSepIdx == -1 || menuIdx >= topSepIdx {
		t.Fatalf("slash menu (line %d) must appear directly ABOVE top separator (line %d)", menuIdx, topSepIdx)
	}
}
