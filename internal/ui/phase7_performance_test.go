package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRepeatedIdenticalResizeSkipsRefresh(t *testing.T) {
	f := NewFeed()
	f.BuildFromEvents([]agent.Event{{Seq: 1, Type: agent.UserMsg, Text: "hello"}})
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	before := f.renderCount
	for i := 0; i < 100; i++ {
		f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	}
	if f.renderCount != before {
		t.Fatalf("identical resize rendered %d extra items", f.renderCount-before)
	}
}

func TestRenderedViewportBackingIsBounded(t *testing.T) {
	f := NewFeed()
	f.BuildFromEvents([]agent.Event{{Seq: 1, Type: agent.AssistantText, Text: strings.Repeat("line\n", maxRenderedFeedLines+2000)}})
	if len(f.lines) > maxRenderedFeedLines {
		t.Fatalf("retained %d lines, limit %d", len(f.lines), maxRenderedFeedLines)
	}
}

func TestLineCacheNeverExceedsVisibleItemCap(t *testing.T) {
	f := NewFeed()
	events := make([]agent.Event, 0, maxVisibleFeedItems+100)
	for i := 0; i < maxVisibleFeedItems+100; i++ {
		events = append(events, agent.Event{Seq: i + 1, Type: agent.UserMsg, Text: fmt.Sprintf("item %d", i)})
	}
	f.BuildFromEvents(events)
	if len(f.lineCache) > maxVisibleFeedItems {
		t.Fatalf("cache has %d entries, cap %d", len(f.lineCache), maxVisibleFeedItems)
	}
}

func BenchmarkResizeAndStreamingRefresh(b *testing.B) {
	f := NewFeed()
	f.BuildFromEvents([]agent.Event{{Seq: 1, Type: agent.AssistantText, Text: strings.Repeat("stream ", 200)}})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.Update(tea.WindowSizeMsg{Width: 79 + i%2, Height: 24})
	}
}
