package ui

import (
	"fmt"
	"strings"
	"testing"

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
		_ = renderItemsCached(f, items, 80, false)
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
