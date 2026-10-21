package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/event"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func acceptanceEvents() []event.Event {
	read := &event.ToolCall{ID: "read-1", Name: "read_file", Args: []byte(`{"path":"src/ملف.go"}`)}
	failed := &event.ToolCall{ID: "bash-1", Name: "bash", Args: []byte(`{"cmd":"go test ./..."}`)}
	permission := &event.ToolCall{ID: "perm-1", Name: "bash", Args: []byte(`{"cmd":"rm build.tmp"}`)}
	return []event.Event{
		{Seq: 1, Type: event.UserMsg, Text: "افحص الملف src/ملف.go"},
		{Seq: 2, Type: event.TextDelta, Text: "Checking the file.\nSecond line."},
		{Seq: 3, Type: event.ToolStart, Call: read},
		{Seq: 4, Type: event.ToolEnd, Call: &event.ToolCall{ID: read.ID, Name: read.Name, Args: read.Args, OK: true, Output: "1|package main\n...[truncated 80 bytes]\nnext_offset=170"}},
		{Seq: 5, Type: event.ToolStart, Call: failed},
		{Seq: 6, Type: event.ToolEnd, Call: &event.ToolCall{ID: failed.ID, Name: failed.Name, Args: failed.Args, OK: false, Exit: 1, Output: "tests failed"}},
		{Seq: 7, Type: event.PermAsk, Call: permission},
		{Seq: 8, Type: event.PermReply, Call: permission, Decision: event.Deny, RawDecision: event.Deny},
		{Seq: 9, Type: event.RunError, Err: "provider unavailable", ErrorCode: string(event.ErrCodeProviderTemporary)},
	}
}

func TestProductAcceptanceMatrix(t *testing.T) {
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			f := NewFeed()
			f.Update(tea.WindowSizeMsg{Width: width, Height: 40})
			f.BuildFromEvents(acceptanceEvents())
			feed := ansi.Strip(strings.Join(f.lines, "\n"))
			for _, line := range strings.Split(feed, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("line width %d exceeds %d: %q", ansi.StringWidth(line), width, line)
				}
			}
			if !strings.Contains(feed, "action:") {
				t.Errorf("missing next action in feed:\n%s", feed)
			}
			if width >= 40 {
				for _, want := range []string{"Nabd", "Read", "Bash", "provider"} {
					if !strings.Contains(feed, want) {
						t.Errorf("missing semantic cue %q in feed:\n%s", want, feed)
					}
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
	for _, ev := range events {
		live.applyBatch([]event.Event{ev})
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
	events := make([]event.Event, 1000)
	for i := range events {
		events[i] = event.Event{Seq: i + 1, Type: event.UserMsg, Text: fmt.Sprintf("message %d", i)}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f := NewFeed()
		f.BuildFromEvents(events)
	}
}

func BenchmarkAcceptanceReplay10000(b *testing.B) {
	events := make([]event.Event, 10000)
	for i := range events {
		events[i] = event.Event{Seq: i + 1, Type: event.UserMsg, Text: fmt.Sprintf("message %d", i)}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f := NewFeed()
		f.BuildFromEvents(events)
	}
}
