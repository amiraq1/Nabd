package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"
)

func itemAtLinear(m *Feed, line int) int {
	if line < 0 || line >= len(m.lines) {
		return -1
	}
	found := -1
	for i, o := range m.offsets {
		if o <= line {
			found = i
		} else {
			break
		}
	}
	return found
}

func feedWithItems(t *testing.T, count, width, linesPerItem int) *Feed {
	t.Helper()
	m := NewFeed()
	m.width = width
	events := make([]agent.Event, 0, count*2)
	for i := 0; i < count; i++ {
		var lines []string
		for j := 0; j < linesPerItem; j++ {
			lines = append(lines, fmt.Sprintf("item %d line %d", i, j))
		}
		events = append(events,
			agent.Event{
				Seq:  i*2 + 1,
				Type: agent.ToolStart,
				Call: &agent.ToolCall{ID: fmt.Sprintf("c%d", i), Name: "bash", Args: []byte(`"echo test"`)},
			},
			agent.Event{
				Seq:  i*2 + 2,
				Type: agent.ToolEnd,
				Call: &agent.ToolCall{ID: fmt.Sprintf("c%d", i), Name: "bash", Output: strings.Join(lines, "\n"), OK: true},
			},
		)
	}
	m.applyBatch(events)
	m.refresh()
	return m
}

func TestShiftOffsetsDropsLeadingAndClampsToZero(t *testing.T) {
	offsets := []int{0, 5, 10, 25, 50}
	trimmed := 10
	shifted := shiftOffsets(offsets, trimmed)
	want := []int{0, 0, 0, 15, 40}
	if len(shifted) != len(want) {
		t.Fatalf("len mismatch: got %d, want %d", len(shifted), len(want))
	}
	for i := range want {
		if shifted[i] != want[i] {
			t.Errorf("shifted[%d] = %d, want %d", i, shifted[i], want[i])
		}
	}
	// Zero trimmed
	untrimmed := shiftOffsets(offsets, 0)
	for i := range offsets {
		if untrimmed[i] != offsets[i] {
			t.Errorf("untrimmed[%d] = %d, want %d", i, untrimmed[i], offsets[i])
		}
	}
}

func TestItemAtBinarySearchMatchesLinear(t *testing.T) {
	m := feedWithItems(t, 20, 80, 5)
	if len(m.lines) == 0 {
		t.Fatal("feed has no lines")
	}
	for l := 0; l < len(m.lines); l++ {
		got := m.itemAt(l)
		want := itemAtLinear(m, l)
		if got != want {
			t.Fatalf("itemAt(%d) = %d, want %d", l, got, want)
		}
	}
	if m.itemAt(-1) != -1 {
		t.Fatalf("itemAt(-1) = %d, want -1", m.itemAt(-1))
	}
	if m.itemAt(len(m.lines)) != -1 {
		t.Fatalf("itemAt(out of bounds) = %d, want -1", m.itemAt(len(m.lines)))
	}
}

func TestItemAtBoundRetentionTrimming(t *testing.T) {
	m := NewFeed()
	m.width = 80
	// Create two items with many lines exceeding maxRenderedFeedLines (12000)
	events := []agent.Event{
		{
			Seq:  1,
			Type: agent.UserMsg,
			Text: strings.Repeat("item 0 line\n", 8000),
		},
		{
			Seq:  2,
			Type: agent.UserMsg,
			Text: strings.Repeat("item 1 line\n", 8000),
		},
	}
	m.applyBatch(events)
	m.refresh()

	if len(m.lines) > maxRenderedFeedLines {
		t.Fatalf("lines %d exceeded retention cap %d", len(m.lines), maxRenderedFeedLines)
	}
	for i, o := range m.offsets {
		if o >= len(m.lines) {
			t.Fatalf("offset %d out of range: %d (lines=%d)", i, o, len(m.lines))
		}
		if i > 0 && o < m.offsets[i-1] {
			t.Fatalf("offsets not monotonic: %d < %d", o, m.offsets[i-1])
		}
	}
	for i, o := range m.offsets {
		if o >= len(m.lines) {
			t.Fatalf("offset %d not rebased: %d (lines=%d)", i, o, len(m.lines))
		}
	}
	if m.itemAt(0) < 0 {
		t.Fatalf("itemAt(0) = %d, want >= 0", m.itemAt(0))
	}
}

func TestSelectItemUsesStoredOffsets(t *testing.T) {
	m := feedWithItems(t, 20, 80, 5)
	items := m.navigationItems()
	for idx := 0; idx < len(items); idx++ {
		m.selectItem(idx)
		if m.scrollTop >= len(m.lines) {
			t.Fatalf("select %d: scrollTop=%d out of rendered range %d",
				idx, m.scrollTop, len(m.lines))
		}
	}
}

func TestOffsetsSyncedAfterToggleTools(t *testing.T) {
	m := feedWithItems(t, 20, 40, 3)
	m.follow = false
	m.scrollTop = 5
	if _, _ = m.toggleTools(); len(m.offsets) == 0 {
		t.Fatal("toggleTools left offsets empty")
	}
	for i, o := range m.offsets {
		if o >= len(m.lines) {
			t.Fatalf("stale offset %d after toggle: %d (lines=%d)", i, o, len(m.lines))
		}
	}
	if got, want := m.itemAt(m.scrollTop), itemAtLinear(m, m.scrollTop); got != want {
		t.Fatalf("itemAt disagrees after toggle: %d vs %d", got, want)
	}
}

// --- status precedence ---

func TestHintNeverMasksActiveRun(t *testing.T) {
	m := NewFeed()
	m.width, m.height = 80, 24
	m.running, m.busy = true, true
	m.enterNavigation()
	if got := m.runtimeStatusText(); !strings.Contains(got, "Generating") {
		t.Fatalf("navigation hint masked the active run: %q", got)
	}
}

func TestCommandResultNeverMasksActiveRun(t *testing.T) {
	m := NewFeed()
	m.width, m.height = 80, 24
	m.running, m.busy = true, true
	m.setStatus("context: 12k tokens", rankResult)
	if got := m.runtimeStatusText(); !strings.Contains(got, "Generating") {
		t.Fatalf("command result masked the active run: %q", got)
	}
}

func TestRunLifecycleOutranksPhase(t *testing.T) {
	m := NewFeed()
	m.running, m.busy = true, true
	m.setStatus("canceling…", rankRunLifecycle)
	if got := m.runtimeStatusText(); got != "canceling…" {
		t.Fatalf("cancel notice lost to phase text: %q", got)
	}
}

func TestPermissionOutranksEverything(t *testing.T) {
	m := NewFeed()
	m.running, m.busy = true, true
	m.setStatus("canceling…", rankRunLifecycle)
	m.modalVisible = true
	if got := m.runtimeStatusText(); got != "Permission Required" {
		t.Fatalf("permission was masked: %q", got)
	}
	m.modalVisible, m.decisionPending = false, true
	if got := m.runtimeStatusText(); got != "Waiting for permission…" {
		t.Fatalf("pending decision was masked: %q", got)
	}
}

func TestHintShowsWhenIdle(t *testing.T) {
	m := NewFeed()
	m.width, m.height = 80, 24
	m.enterNavigation()
	if got := m.runtimeStatusText(); got == "" {
		t.Fatal("idle feed dropped the navigation hint")
	}
}

func TestLowerRankDoesNotOverwriteHigher(t *testing.T) {
	m := NewFeed()
	m.setStatus("canceling…", rankRunLifecycle)
	m.setStatus("nav hint", rankHint)
	if m.status != "canceling…" {
		t.Fatalf("hint overwrote a run-lifecycle notice: %q", m.status)
	}
	m.clearStatus()
	m.setStatus("nav hint", rankHint)
	if m.status != "nav hint" {
		t.Fatalf("clearStatus did not release the row: %q", m.status)
	}
}

// Parallel tool calls are distinguished by CallID, not by name.
func TestParallelToolsReportedByCount(t *testing.T) {
	m := NewFeed()
	m.running, m.busy = true, true
	for _, id := range []string{"c1", "c2"} {
		m.statusProj.Apply(agent.Event{
			Type: agent.ToolStart,
			Call: &agent.ToolCall{ID: id, Name: "bash"},
		})
	}
	if got := m.runtimeStatusText(); !strings.Contains(got, "2 tools") {
		t.Fatalf("parallel tools collapsed to one: %q", got)
	}
}

func TestSingleToolNamedInStatus(t *testing.T) {
	m := NewFeed()
	m.running, m.busy = true, true
	m.statusProj.Apply(agent.Event{
		Type: agent.ToolStart,
		Call: &agent.ToolCall{ID: "c1", Name: "read_file"},
	})
	if got := m.runtimeStatusText(); !strings.Contains(got, "read_file") {
		t.Fatalf("single tool not named: %q", got)
	}
}
