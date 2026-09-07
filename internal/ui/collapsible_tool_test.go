package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestCollapsibleTool_SingleLineStatuses verifies that all tool statuses render
// as a single compact summary row when collapsed, and output is omitted.
func TestCollapsibleTool_SingleLineStatuses(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	tests := []struct {
		name     string
		status   presentation.ToolStatus
		toolName string
		args     string
		duration int64
		exitCode int
		wantSym  string
		wantName string
		wantMeta string
		output   string
	}{
		{
			name:     "pending",
			status:   presentation.ToolPending,
			toolName: "read_file",
			args:     "internal/ui/feed.go",
			wantSym:  "o",
			wantName: "Read",
			output:   "long file content that should not be visible",
		},
		{
			name:     "running",
			status:   presentation.ToolRunning,
			toolName: "bash",
			args:     "go test ./internal/ui",
			wantSym:  "~",
			wantName: "Bash",
			output:   "running output line 1\nline 2",
		},
		{
			name:     "done",
			status:   presentation.ToolDone,
			toolName: "read_file",
			args:     "internal/ui/feed.go",
			duration: 8,
			wantSym:  "✓",
			wantName: "Read",
			wantMeta: "8ms",
			output:   "file contents here\nline 2",
		},
		{
			name:     "failed",
			status:   presentation.ToolFailed,
			toolName: "bash",
			args:     "go test",
			duration: 120,
			exitCode: 1,
			wantSym:  "✗",
			wantName: "Bash",
			wantMeta: "exit 1 · 120ms",
			output:   "FAIL: TestSomething\nexit status 1",
		},
		{
			name:     "denied",
			status:   presentation.ToolDenied,
			toolName: "bash",
			args:     "rm -rf /",
			wantSym:  "✗",
			wantName: "Bash",
			output:   "permission denied by user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := presentation.FeedItem{
				Type: presentation.ItemTool,
				Tool: &presentation.ToolCard{
					Name:     tt.toolName,
					Args:     tt.args,
					Status:   tt.status,
					Duration: tt.duration,
					ExitCode: tt.exitCode,
					Output:   tt.output,
				},
			}

			lines := renderTool(it, 60, false)
			if len(lines) != 1 {
				t.Fatalf("expected 1 summary line for collapsed tool, got %d: %v", len(lines), lines)
			}

			plain := ansi.Strip(lines[0])
			if !strings.Contains(plain, tt.wantSym) {
				t.Errorf("summary line missing symbol %q: %q", tt.wantSym, plain)
			}
			if !strings.Contains(plain, tt.wantName) {
				t.Errorf("summary line missing name %q: %q", tt.wantName, plain)
			}
			if tt.args != "" && !strings.Contains(plain, tt.args) {
				t.Errorf("summary line missing args %q: %q", tt.args, plain)
			}
			if tt.wantMeta != "" && !strings.Contains(plain, tt.wantMeta) {
				t.Errorf("summary line missing metadata %q: %q", tt.wantMeta, plain)
			}
			if tt.output != "" && strings.Contains(plain, tt.output) {
				t.Errorf("collapsed tool leaked tool output: %q", plain)
			}
		})
	}
}

// TestCollapsibleTool_SimultaneousDurationAndExitCode verifies simultaneous
// reporting of nonzero exit code / signal and duration without hiding either.
func TestCollapsibleTool_SimultaneousDurationAndExitCode(t *testing.T) {
	// Exit code + duration
	itExit := presentation.FeedItem{
		Type: presentation.ItemTool,
		Tool: &presentation.ToolCard{
			Name:     "bash",
			Args:     "run.sh",
			Status:   presentation.ToolFailed,
			ExitCode: 1,
			Duration: 120,
		},
	}
	linesExit := renderTool(itExit, 60, false)
	if len(linesExit) != 1 {
		t.Fatalf("expected 1 line, got %d", len(linesExit))
	}
	plainExit := ansi.Strip(linesExit[0])
	if !strings.Contains(plainExit, "exit 1") || !strings.Contains(plainExit, "120ms") {
		t.Errorf("expected both 'exit 1' and '120ms' in summary line: %q", plainExit)
	}

	// Signal + duration
	itSig := presentation.FeedItem{
		Type: presentation.ItemTool,
		Tool: &presentation.ToolCard{
			Name:     "bash",
			Args:     "sleep 10",
			Status:   presentation.ToolFailed,
			Signal:   "killed",
			Duration: 250,
		},
	}
	linesSig := renderTool(itSig, 60, false)
	if len(linesSig) != 1 {
		t.Fatalf("expected 1 line, got %d", len(linesSig))
	}
	plainSig := ansi.Strip(linesSig[0])
	if !strings.Contains(plainSig, "killed") || !strings.Contains(plainSig, "250ms") {
		t.Errorf("expected both 'killed' and '250ms' in summary line: %q", plainSig)
	}
}

// TestCollapsibleTool_FailureErrorPreservation verifies that failed tools
// with an error preserve a concise sanitized error line in both collapsed and expanded states.
func TestCollapsibleTool_FailureErrorPreservation(t *testing.T) {
	it := presentation.FeedItem{
		Type: presentation.ItemTool,
		Tool: &presentation.ToolCard{
			Name:     "read_file",
			Args:     "missing.go",
			Status:   presentation.ToolFailed,
			ExitCode: 1,
			Duration: 15,
			Err:      "file not found: missing.go",
			Output:   "open missing.go: no such file or directory\nstack trace line 1\nstack trace line 2",
		},
	}

	// Collapsed state: summary line + concise error line (2 lines total). Tool output omitted.
	linesCol := renderTool(it, 60, false)
	if len(linesCol) != 2 {
		t.Fatalf("expected 2 lines in collapsed state with error, got %d: %v", len(linesCol), linesCol)
	}
	plainColSummary := ansi.Strip(linesCol[0])
	plainColErr := ansi.Strip(linesCol[1])

	if !strings.Contains(plainColSummary, "Read") || !strings.Contains(plainColSummary, "missing.go") {
		t.Errorf("summary line missing tool info: %q", plainColSummary)
	}
	if !strings.Contains(plainColErr, "file not found: missing.go") {
		t.Errorf("error line missing concise error: %q", plainColErr)
	}
	if strings.Contains(strings.Join(linesCol, "\n"), "stack trace") {
		t.Errorf("collapsed tool leaked full output")
	}

	// Expanded state: summary line + concise error line + output lines.
	linesExp := renderTool(it, 60, true)
	if len(linesExp) <= 2 {
		t.Fatalf("expected expanded output to have > 2 lines, got %d", len(linesExp))
	}
	plainExp := ansi.Strip(strings.Join(linesExp, "\n"))
	if !strings.Contains(plainExp, "file not found: missing.go") {
		t.Errorf("expanded output missing concise error line: %q", plainExp)
	}
	if !strings.Contains(plainExp, "open missing.go: no such file or directory") {
		t.Errorf("expanded output missing tool output: %q", plainExp)
	}
}

// TestCollapsibleTool_DisplayNames verifies presentation tool name mapping.
func TestCollapsibleTool_DisplayNames(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"read_file", "Read"},
		{"write_file", "Write"},
		{"edit_file", "Edit"},
		{"bash", "Bash"},
		{"grep", "Grep"},
		{"glob", "Glob"},
		{"custom_tool", "custom_tool"},
		{"web_search", "web_search"},
	}

	for _, c := range cases {
		got := toolDisplayName(c.in)
		if got != c.want {
			t.Errorf("toolDisplayName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCollapsibleTool_ToggleCtrlO verifies Ctrl+O toggles Feed.toolsExpanded.
func TestCollapsibleTool_ToggleCtrlO(t *testing.T) {
	f := NewFeed()
	if f.ToolsExpanded() {
		t.Fatal("expected toolsExpanded to be false by default")
	}

	// First Ctrl+O: expand
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !f.ToolsExpanded() {
		t.Fatal("expected toolsExpanded to be true after Ctrl+O")
	}

	// Second Ctrl+O: collapse
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if f.ToolsExpanded() {
		t.Fatal("expected toolsExpanded to be false after second Ctrl+O")
	}
}

// TestCollapsibleTool_ComposerFocusSafety verifies that Ctrl+O does not mutate
// the composer text, change cursor position, or trigger history browsing.
func TestCollapsibleTool_ComposerFocusSafety(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.composer.setValue("initial composer draft")

	// Send Ctrl+O
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})

	if !f.ToolsExpanded() {
		t.Fatal("expected toolsExpanded to be true")
	}
	if f.composer.value() != "initial composer draft" {
		t.Fatalf("composer text mutated: got %q", f.composer.value())
	}
	if f.HistoryBrowsing() {
		t.Fatal("history browsing unexpectedly activated")
	}
}

// TestCollapsibleTool_PasteGuard verifies that pasted content containing
// the Ctrl+O byte (0x0f) does NOT trigger tool toggling.
func TestCollapsibleTool_PasteGuard(t *testing.T) {
	f := NewFeed()
	if f.ToolsExpanded() {
		t.Fatal("expected toolsExpanded to be false")
	}

	// Pasted KeyCtrlO
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO, Paste: true})
	if f.ToolsExpanded() {
		t.Fatal("toolsExpanded toggled on pasted KeyCtrlO")
	}

	// Pasted rune 0x0f
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0x0f}, Paste: true})
	if f.ToolsExpanded() {
		t.Fatal("toolsExpanded toggled on pasted rune 0x0f")
	}
}

// TestCollapsibleTool_ModalAndMenuSafety verifies that Ctrl+O is not intercepted
// when the permission modal or slash command menu owns input focus.
func TestCollapsibleTool_ModalAndMenuSafety(t *testing.T) {
	f := NewFeed()

	// 1. Permission modal active
	f.modalVisible = true
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if f.ToolsExpanded() {
		t.Fatal("Ctrl+O intercepted while permission modal was visible")
	}
	f.modalVisible = false

	// 2. Slash menu active
	f.menu.open(supportedSlashCommands)
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if f.ToolsExpanded() {
		t.Fatal("Ctrl+O intercepted while slash menu was open")
	}
}

// TestCollapsibleTool_StreamingUpdatesNoDuplicates verifies that transition
// from ToolStart to ToolEnd updates the existing card in place without duplicate lines.
func TestCollapsibleTool_StreamingUpdatesNoDuplicates(t *testing.T) {
	f := NewFeed()
	f.width = 60

	// ToolStart
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`"make"`)}},
	}})
	if len(f.lines) != 1 {
		t.Fatalf("expected 1 line for running tool, got %d: %v", len(f.lines), f.lines)
	}
	if !strings.Contains(ansi.Strip(f.lines[0]), "~") {
		t.Errorf("expected running symbol ~ in line: %q", f.lines[0])
	}

	// ToolEnd
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "bash", Output: "build complete", OK: true, MS: 50}},
	}})
	if len(f.lines) != 1 {
		t.Fatalf("expected still exactly 1 line for completed tool, got %d: %v", len(f.lines), f.lines)
	}
	if !strings.Contains(ansi.Strip(f.lines[0]), "✓") || !strings.Contains(ansi.Strip(f.lines[0]), "50ms") {
		t.Errorf("expected done symbol and duration in line: %q", f.lines[0])
	}
}

// TestCollapsibleTool_AnchorAndUnseenPreservation verifies that:
// 1. In follow mode, toggle re-anchors to bottom.
// 2. In browsing history mode (!follow), toggle preserves visible content anchor.
// 3. Toggling alone does NOT increment unseen.
func TestCollapsibleTool_AnchorAndUnseenPreservation(t *testing.T) {
	f := NewFeed()
	f.width = 60
	f.height = 15

	var longOutput strings.Builder
	for i := 1; i <= 20; i++ {
		longOutput.WriteString(fmt.Sprintf("tool output row %02d\n", i))
	}

	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "message 1"},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "read_file", Args: json.RawMessage(`"a.go"`)}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "read_file", Output: longOutput.String(), OK: true}},
		{Seq: 4, Type: agent.UserMsg, Text: "message 2"},
	}})

	// Follow active: expand and collapse keep follow = true
	if !f.follow {
		t.Fatal("expected follow to be true initially")
	}
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !f.follow {
		t.Errorf("expected follow to remain true after expand")
	}
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !f.follow {
		t.Errorf("expected follow to remain true after collapse")
	}

	// Browsing history (!follow)
	f.follow = false
	f.scrollTop = 0
	f.unseen = 2

	// Toggle expand
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if f.follow {
		t.Errorf("follow unexpectedly became true while browsing")
	}
	if f.unseen != 2 {
		t.Errorf("unseen changed during toggle: got %d, want 2", f.unseen)
	}

	// Toggle collapse
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if f.follow {
		t.Errorf("follow unexpectedly became true while browsing")
	}
	if f.unseen != 2 {
		t.Errorf("unseen changed during toggle: got %d, want 2", f.unseen)
	}
}

// TestCollapsibleTool_NarrowWidths verifies that renderTool obeys terminal
// width constraints and prioritizes status and tool identity.
func TestCollapsibleTool_NarrowWidths(t *testing.T) {
	it := presentation.FeedItem{
		Type: presentation.ItemTool,
		Tool: &presentation.ToolCard{
			Name:     "bash",
			Args:     "really_long_command_with_many_arguments_that_exceeds_screen.sh",
			Status:   presentation.ToolFailed,
			ExitCode: 1,
			Duration: 120,
		},
	}

	for _, w := range []int{50, 30, 20, 10} {
		lines := renderTool(it, w, false)
		if len(lines) != 1 {
			t.Fatalf("width %d: expected 1 line, got %d", w, len(lines))
		}
		actualWidth := ansi.StringWidth(lines[0])
		if actualWidth > w {
			t.Errorf("width %d: rendered line width %d exceeded constraint: %q", w, actualWidth, lines[0])
		}
		plain := ansi.Strip(lines[0])
		if w >= 20 {
			if !strings.Contains(plain, "Bash") || !strings.Contains(plain, "✗") {
				t.Errorf("width %d: expected tool identity and status preserved: %q", w, plain)
			}
		}
	}
}

// TestCollapsibleTool_FooterHint verifies footer text includes Ctrl+O hints
// when tool cards are present, and excludes them when no tools exist.
func TestCollapsibleTool_FooterHint(t *testing.T) {
	f := newFeedAt(t, 80, 24)

	// No tools initially
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	}})
	v := f.View()
	if strings.Contains(v, "Ctrl+O") || strings.Contains(v, "^O") {
		t.Errorf("footer has tool hint when no tools exist:\n%s", v)
	}

	// Tool added (collapsed by default)
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash"}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash", OK: true}},
	}})
	v = f.View()
	if !strings.Contains(v, "Ctrl+O details") && !strings.Contains(v, "^O details") {
		t.Errorf("footer missing 'details' hint for collapsed tool:\n%s", v)
	}

	// Toggle expanded
	f.SetToolsExpanded(true)
	v = f.View()
	if !strings.Contains(v, "Ctrl+O collapse") && !strings.Contains(v, "^O collapse") {
		t.Errorf("footer missing 'collapse' hint for expanded tool:\n%s", v)
	}
}

// TestCollapsibleTool_PTYInteractive verifies end-to-end PTY behavior:
// collapsed single-line initial rendering, Ctrl+O expansion toggle via \x0f,
// and composer interactivity.
func TestCollapsibleTool_PTYInteractive(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	var sb strings.Builder
	for i := 1; i <= 5; i++ {
		sb.WriteString(fmt.Sprintf("DETAILED_OUTPUT_ROW_%d\n", i))
	}

	sess.InjectBatch([]agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "read_file", Args: []byte(`{"path":"main.go"}`)}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "read_file", Output: sb.String(), OK: true, MS: 12}},
	})

	// 1. Initial collapsed state: summary line visible, details hidden
	err := sess.WaitForText("Read", 3*time.Second)
	if err != nil {
		t.Fatalf("summary line did not appear: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)
	if snap.Contains("DETAILED_OUTPUT_ROW_1") {
		t.Fatalf("tool output should be collapsed, but found detailed output on screen:\n%s", snap.PlainText())
	}
	if !snap.Contains("Ctrl+O details") {
		t.Fatalf("footer missing Ctrl+O details hint:\n%s", snap.PlainText())
	}

	// 2. Send \x0f (Ctrl+O) to expand
	sess.SendKey([]byte{0x0f})
	err = sess.WaitForText("DETAILED_OUTPUT_ROW_1", 3*time.Second)
	if err != nil {
		t.Fatalf("details failed to expand after Ctrl+O: %v", err)
	}

	snapExp := sess.Snapshot()
	if !snapExp.Contains("Ctrl+O collapse") {
		t.Fatalf("footer missing Ctrl+O collapse hint:\n%s", snapExp.PlainText())
	}

	// 3. Send \x0f (Ctrl+O) again to collapse
	sess.SendKey([]byte{0x0f})
	err = sess.WaitForCondition("details collapsed", 3*time.Second, func(s ScreenSnapshot) bool {
		return !s.Contains("DETAILED_OUTPUT_ROW_1")
	})
	if err != nil {
		t.Fatalf("details failed to collapse after second Ctrl+O: %v", err)
	}

	// 4. Type into composer to verify keyboard interactivity
	sess.WriteString("hello composer")
	err = sess.WaitForText("hello composer", 2*time.Second)
	if err != nil {
		t.Fatalf("composer typing failed: %v", err)
	}
}
