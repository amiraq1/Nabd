package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

func BenchmarkRefreshStreaming(b *testing.B) {
	// 500 items with unique IDs (the cache key); the last assistant item
	// carries accumulated streaming text.
	const n = 500
	f := &Feed{}
	f.width = 80
	items := make([]presentation.FeedItem, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, presentation.FeedItem{Type: presentation.ItemUserMsg, ID: fmt.Sprintf("u%d", i), Text: fmt.Sprintf("user %d", i)})
		items = append(items, presentation.FeedItem{Type: presentation.ItemAssistant, ID: fmt.Sprintf("a%d", i), Text: fmt.Sprintf("assistant reply %d", i)})
	}
	last := &items[len(items)-1]
	base := last.Text
	const delta = "streaming delta that accumulates during the assistant response. "
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		last.Text = base + strings.Repeat(delta, 50)
		_, _ = renderItemsCached(f, items, 80, false)
	}
}

func BenchmarkFormatInlineLong(b *testing.B) {
	// ~20KB text with 200 bold (**) pairs.
	var sb strings.Builder
	sb.Grow(20 * 1024)
	for i := 0; i < 200; i++ {
		sb.WriteString(fmt.Sprintf("segment %03d with some leading prose **bold text here** and some trailing prose to pad the length ", i))
	}
	text := sb.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = formatInline(text)
	}
}

func BenchmarkFormatMarkdown(b *testing.B) {
	text := strings.Repeat("# Heading\n\nSome **bold** text and `inline code`.\n- first item\n- second item\n", 6)
	for i := 0; i < b.N; i++ {
		_ = formatMarkdown(text, 60)
	}
}

// BenchmarkRenderItems500 measures the cost of rendering 500 mixed
// user/assistant items through renderItems (the non-cached path).
func BenchmarkRenderItems500(b *testing.B) {
	items := make([]presentation.FeedItem, 0, 500)
	for i := 0; i < 250; i++ {
		items = append(items, presentation.FeedItem{
			Type: presentation.ItemUserMsg,
			Text: fmt.Sprintf("user message %d with some **bold** text and `code` inline", i),
		})
		items = append(items, presentation.FeedItem{
			Type: presentation.ItemAssistant,
			Text: fmt.Sprintf("assistant reply %d with some **bold** text for emphasis", i),
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = renderItems(items, 80, false)
	}
}

// BenchmarkFormatMarkdownLong renders a large Markdown payload repeatedly.
func BenchmarkFormatMarkdownLong(b *testing.B) {
	text := strings.Repeat("# Heading\n\nSome **bold** text and `inline code`.\n- first item\n- second item\n", 50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = formatMarkdown(text, 60)
	}
}

func BenchmarkViewFullScreen(b *testing.B) {
	f := NewFeed()
	f.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	for i := 0; i < 40; i++ {
		f.lines = append(f.lines, fmt.Sprintf("viewport line %d: content here for benchmarking", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = f.View()
	}
}

func BenchmarkViewFullScreenWithLiveThroughput(b *testing.B) {
	f := NewFeed()
	f.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	for i := 0; i < 40; i++ {
		f.lines = append(f.lines, fmt.Sprintf("viewport line %d: content here for benchmarking", i))
	}
	f.running = true
	f.busy = true
	start := time.Now()
	f.reqStartedAt = start.Add(-2 * time.Second)
	f.streamStartedAt = start.Add(-1500 * time.Millisecond)
	f.streamFirstDeltaAt = start.Add(-1000 * time.Millisecond)
	f.streamLastDeltaAt = start.Add(-100 * time.Millisecond)
	f.streamedChars = 500
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = f.View()
	}
}

func BenchmarkThroughputBatchedDeltas(b *testing.B) {
	t0 := time.Now()
	const batchSize = 10
	events := make([]agent.Event, batchSize)
	for i := 0; i < batchSize; i++ {
		events[i] = agent.Event{
			Seq:  i + 1,
			Type: agent.TextDelta,
			Text: "streaming chunk with prose ",
			Time: t0.Add(time.Duration(i*25) * time.Millisecond),
		}
	}
	f := &Feed{running: true, busy: true}
	f.trackState(agent.Event{Type: agent.TurnStart, Time: t0})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset per-turn state each iteration to exercise the 200ms throttle boundary
		f.streamFirstDeltaAt = time.Time{}
		f.streamLastDeltaAt = time.Time{}
		f.streamedChars = 0
		f.lastThroughputAt = time.Time{}
		for _, e := range events {
			f.trackState(e)
		}
	}
}
