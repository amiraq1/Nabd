package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"nabd/internal/agent"
)

// TestInvariantNoRowExceedsWidthAllStages verifies that across all six
// standard terminal widths and all lifecycle stages (idle, generating,
// tool running, navigation mode, permission modal, error state), no single
// rendered line in View() exceeds the terminal width.
func TestInvariantNoRowExceedsWidthAllStages(t *testing.T) {
	widths := []int{20, 40, 60, 80, 100, 120}

	stages := []struct {
		name  string
		setup func(f *Feed)
	}{
		{
			name: "idle",
			setup: func(f *Feed) {
				// idle baseline with typical conversation
				f.applyBatch([]agent.Event{
					{Seq: 1, Type: agent.UserMsg, Text: "list files in repo"},
					{Seq: 2, Type: agent.TextDelta, Text: "Here is the listing of the repository files."},
				})
			},
		},
		{
			name: "generating",
			setup: func(f *Feed) {
				f.running = true
				f.busy = true
			},
		},
		{
			name: "single_tool_running",
			setup: func(f *Feed) {
				f.running = true
				f.busy = true
				f.statusProj.Apply(agent.Event{
					Type: agent.ToolStart,
					Call: &agent.ToolCall{ID: "c1", Name: "read_file"},
				})
			},
		},
		{
			name: "parallel_tools_running",
			setup: func(f *Feed) {
				f.running = true
				f.busy = true
				f.statusProj.Apply(agent.Event{
					Type: agent.ToolStart,
					Call: &agent.ToolCall{ID: "c1", Name: "bash"},
				})
				f.statusProj.Apply(agent.Event{
					Type: agent.ToolStart,
					Call: &agent.ToolCall{ID: "c2", Name: "read_file"},
				})
			},
		},
		{
			name: "navigation_mode",
			setup: func(f *Feed) {
				f.applyBatch([]agent.Event{
					{Seq: 1, Type: agent.UserMsg, Text: "test navigation"},
				})
				f.enterNavigation()
			},
		},
		{
			name: "permission_modal",
			setup: func(f *Feed) {
				f.running = true
				f.busy = true
				f.modalVisible = true
				f.permModal.open(&agent.ToolCall{ID: "p1", Name: "bash", Args: []byte(`"rm -rf tmp"`)})
			},
		},
		{
			name: "error_state",
			setup: func(f *Feed) {
				f.setStatus(runFailedStatus, rankRunLifecycle)
			},
		},
	}

	for _, w := range widths {
		for _, st := range stages {
			t.Run(fmt.Sprintf("w=%d/%s", w, st.name), func(t *testing.T) {
				f := newFeedAt(t, w, 24)
				st.setup(f)
				f.refresh()

				view := f.View()
				lines := strings.Split(view, "\n")
				for idx, line := range lines {
					if lineWidth := ansi.StringWidth(line); lineWidth > w {
						t.Errorf("width %d stage %s line %d exceeds width: got %d, want <= %d: %q",
							w, st.name, idx, lineWidth, w, line)
					}
				}
			})
		}
	}
}
