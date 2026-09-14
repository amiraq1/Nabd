package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFirstFitUnit exercises the core firstFit logic, including net budget,
// ordered selection, and lazy formatting.
func TestFirstFitUnit(t *testing.T) {
	t.Run("empty or non-positive budget", func(t *testing.T) {
		if _, ok := firstFit([]string{"a", "b"}, 0, nil); ok {
			t.Fatal("expected false for budget 0")
		}
		if _, ok := firstFit([]string{"a", "b"}, -5, nil); ok {
			t.Fatal("expected false for negative budget")
		}
		if _, ok := firstFit(nil, 50, nil); ok {
			t.Fatal("expected false for nil variants")
		}
	})

	t.Run("first fit selection without formatting", func(t *testing.T) {
		candidates := []string{
			"very long candidate that exceeds budget",
			"medium length candidate",
			"short",
		}
		got, ok := firstFit(candidates, 25, nil)
		if !ok || got != "medium length candidate" {
			t.Fatalf("want %q, got %q (ok=%v)", "medium length candidate", got, ok)
		}
	})

	t.Run("none fit", func(t *testing.T) {
		candidates := []string{"long text", "medium"}
		if got, ok := firstFit(candidates, 3, nil); ok {
			t.Fatalf("expected false, got %q", got)
		}
	})

	t.Run("lazy formatting stops at first fit", func(t *testing.T) {
		variants := []string{"longest_v", "medium_v", "short_v", "tiny"}
		formatCalls := 0
		got, ok := firstFit(variants, 18, func(v string) string {
			formatCalls++
			return "fmt:" + v // "fmt:longest_v" = 13 chars <= 18 budget
		})
		if !ok || got != "fmt:longest_v" {
			t.Fatalf("want fmt:longest_v, got %q (ok=%v)", got, ok)
		}
		if formatCalls != 1 {
			t.Fatalf("expected exactly 1 format call for immediate fit, got %d", formatCalls)
		}

		// When first variant doesn't fit, format should be called lazily until one fits
		formatCalls = 0
		got, ok = firstFit(variants, 11, func(v string) string {
			formatCalls++
			return "fmt:" + v
			// "fmt:longest_v" = 13 (overflows 11)
			// "fmt:medium_v" = 12 (overflows 11)
			// "fmt:short_v" = 11 (fits <= 11)
		})
		if !ok || got != "fmt:short_v" {
			t.Fatalf("want fmt:short_v, got %q (ok=%v)", got, ok)
		}
		if formatCalls != 3 {
			t.Fatalf("expected exactly 3 format calls, got %d", formatCalls)
		}
	})
}

// TestViewNeverOverflowsWidthAcrossAllWidths is the universal layout guard.
// It verifies that across all widths from 20 to 120, every row rendered by View()
// strictly respects the terminal width budget without horizontal overflow.
func TestViewNeverOverflowsWidthAcrossAllWidths(t *testing.T) {
	scenarios := []struct {
		name  string
		setup func() *Feed
	}{
		{
			name: "idle feed with prompt and message",
			setup: func() *Feed {
				f := NewFeed()
				f.applyBatch([]agent.Event{
					{Seq: 1, Type: agent.UserMsg, Text: "short question"},
					{Seq: 2, Type: agent.TextDelta, Text: "this is an answer with some detail to wrap"},
					{Seq: 3, Type: agent.TurnEnd},
				})
				return f
			},
		},
		{
			name: "live streaming with throughput active",
			setup: func() *Feed {
				f := NewFeed()
				f.running = true
				f.busy = true
				start := time.Now()
				f.reqStartedAt = start
				f.streamStartedAt = start
				f.streamFirstDeltaAt = start.Add(1240 * time.Millisecond)
				f.streamLastDeltaAt = start.Add(2240 * time.Millisecond)
				f.streamedChars = 180
				f.applyBatch([]agent.Event{
					{Seq: 1, Type: agent.UserMsg, Text: "compute streaming"},
					{Seq: 2, Type: agent.TextDelta, Text: "streaming text"},
				})
				return f
			},
		},
		{
			name: "running tool active",
			setup: func() *Feed {
				f := NewFeed()
				f.running = true
				f.applyBatch([]agent.Event{
					{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash", Args: json.RawMessage(`"go test -v ./..."`)}},
				})
				return f
			},
		},
		{
			name: "permission modal visible",
			setup: func() *Feed {
				f := NewFeed()
				f.applyBatch([]agent.Event{
					{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`"rm -rf /tmp/test"`)}},
				})
				return f
			},
		},
		{
			name: "navigation mode browsing history",
			setup: func() *Feed {
				f := feedWithTools(t, 4, 60)
				f.enterNavigation()
				f.selectItem(1)
				return f
			},
		},
		{
			name: "long wrapping content with Arabic diacritics",
			setup: func() *Feed {
				f := NewFeed()
				f.applyBatch([]agent.Event{
					{Seq: 1, Type: agent.UserMsg, Text: "اَلْعَرَبِيَّةُ لُغَةٌ سَامِيَّةٌ جَمِيلَةٌ ذَاتُ تَارِيخٍ عَرِيقٍ وَتَفَاصِيلَ كَثِيرَةٍ"},
					{Seq: 2, Type: agent.TextDelta, Text: "هذا رد طويل يحتوي على تشكيل وتفاصيل متتالية لاختبار التفاف الأسطر بدقة"},
					{Seq: 3, Type: agent.TurnEnd},
				})
				return f
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			for w := 20; w <= 120; w++ {
				f := sc.setup()
				f.Update(tea.WindowSizeMsg{Width: w, Height: 24})
				output := f.View()
				for i, line := range strings.Split(output, "\n") {
					if got := ansi.StringWidth(line); got > w {
						t.Errorf("scenario %q width %d: row %d overflows budget %d (actual %d): %q",
							sc.name, w, i, w, got, line)
					}
				}
			}
		})
	}
}
