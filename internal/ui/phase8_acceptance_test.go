package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func acceptanceEvents() []agent.Event {
	read := &agent.ToolCall{ID: "read-1", Name: "read_file", Args: []byte(`{"path":"src/ملف.go"}`)}
	failed := &agent.ToolCall{ID: "bash-1", Name: "bash", Args: []byte(`{"cmd":"go test ./..."}`)}
	permission := &agent.ToolCall{ID: "perm-1", Name: "bash", Args: []byte(`{"cmd":"rm build.tmp"}`)}
	return []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "افحص الملف src/ملف.go"},
		{Seq: 2, Type: agent.TextDelta, Text: "Checking the file.\nSecond line."},
		{Seq: 3, Type: agent.ToolStart, Call: read},
		{Seq: 4, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: read.ID, Name: read.Name, Args: read.Args, OK: true, Output: "1|package main\n...[truncated 80 bytes]\nnext_offset=170"}},
		{Seq: 5, Type: agent.ToolStart, Call: failed},
		{Seq: 6, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: failed.ID, Name: failed.Name, Args: failed.Args, OK: false, Exit: 1, Err: "tests failed"}},
		{Seq: 7, Type: agent.PermAsk, Call: permission},
		{Seq: 8, Type: agent.PermReply, Call: permission, Decision: agent.Deny, RawDecision: agent.Deny},
		{Seq: 9, Type: agent.RunError, Err: "provider unavailable", ErrorCode: string(agent.ErrProviderTemporary)},
	}
}

func TestProductAcceptanceMatrix(t *testing.T) {
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			f := NewFeed()
			f.Update(tea.WindowSizeMsg{Width: width, Height: 40})
			f.BuildFromEvents(acceptanceEvents())
			view := ansi.Strip(f.View())
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("line width %d exceeds %d: %q", ansi.StringWidth(line), width, line)
				}
			}
			for _, want := range []string{"Nabd", "Read", "Bash", "provider", "action:"} {
				if !strings.Contains(view, want) {
					t.Errorf("missing semantic cue %q in view:\n%s", want, view)
				}
			}
		})
	}
}

func TestLiveReplaySemanticParity(t *testing.T) {
	events := acceptanceEvents()
	replay := NewFeed()
	replay.Update(tea.WindowSizeMsg{Width: 80, Height: 60})
	replay.BuildFromEvents(events)

	live := NewFeed()
	live.Update(tea.WindowSizeMsg{Width: 80, Height: 60})
	for _, event := range events {
		live.applyBatch([]agent.Event{event})
	}
	got := strings.Join(live.lines, "\n")
	want := strings.Join(replay.lines, "\n")
	if got != want {
		t.Fatalf("live/replay mismatch\n--- live ---\n%s\n--- replay ---\n%s", got, want)
	}
}

func TestAcceptanceStatesRemainUnderstandableWithoutColor(t *testing.T) {
	theme := newSemanticTheme(true)
	states := []string{
		theme.Success.Render("✓ success"),
		theme.Running.Render("· running"),
		theme.Warning.Render("! permission"),
		theme.Error.Render("✗ error"),
		theme.Info.Render("· info"),
	}
	for _, state := range states {
		if ansi.Strip(state) != state {
			t.Fatalf("NO_COLOR state emitted ANSI: %q", state)
		}
		if strings.TrimSpace(state) == "" {
			t.Fatal("NO_COLOR removed semantic text")
		}
	}
}

func BenchmarkAcceptanceReplay1000(b *testing.B) {
	events := make([]agent.Event, 1000)
	for i := range events {
		events[i] = agent.Event{Seq: i + 1, Type: agent.UserMsg, Text: fmt.Sprintf("message %d", i)}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f := NewFeed()
		f.BuildFromEvents(events)
	}
}

func BenchmarkAcceptanceReplay10000(b *testing.B) {
	events := make([]agent.Event, 10000)
	for i := range events {
		events[i] = agent.Event{Seq: i + 1, Type: agent.UserMsg, Text: fmt.Sprintf("message %d", i)}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f := NewFeed()
		f.BuildFromEvents(events)
	}
}
