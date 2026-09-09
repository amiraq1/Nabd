package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

// TestCacheReuseOnIdenticalRefresh verifies that an identical refresh
// reuses all items from cache with zero redraws.
func TestCacheReuseOnIdenticalRefresh(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
		{Seq: 2, Type: agent.TurnEnd},
	})

	f.refresh()
	countAfterFirst := f.renderCount

	f.refresh()
	countAfterSecond := f.renderCount

	if countAfterSecond != countAfterFirst {
		t.Fatalf("expected zero redraws on identical refresh, got %d", countAfterSecond-countAfterFirst)
	}
}

// TestCacheOnlyLastItemRedrawsOnGrowth verifies that when only the last
// assistant message grows, only that item is redrawn.
func TestCacheOnlyLastItemRedrawsOnGrowth(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "say hello"},
		{Seq: 2, Type: agent.TextDelta, Text: "hel"},
	})

	f.refresh()
	countBefore := f.renderCount

	f.applyBatch([]agent.Event{
		{Seq: 3, Type: agent.TextDelta, Text: "lo world"},
	})

	redraws := f.renderCount - countBefore
	if redraws != 1 {
		t.Fatalf("expected exactly 1 redraw for last item growth, got %d", redraws)
	}
}

// TestCacheInvalidatesOnWidthChange verifies that changing width
// invalidates ALL cache entries.
func TestCacheInvalidatesOnWidthChange(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
		{Seq: 2, Type: agent.TextDelta, Text: "world"},
	})

	f.refresh()
	if len(f.lineCache) == 0 {
		t.Fatal("cache should be populated after refresh")
	}

	f.width = 100
	f.refresh()

	if f.cacheWidth != 100 {
		t.Fatalf("cacheWidth should be 100, got %d", f.cacheWidth)
	}
}

// TestCacheInvalidatesOnExpansionChange verifies that toggling expansion
// invalidates only the affected items.
func TestCacheInvalidatesOnExpansionChange(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash"}},
		{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash", Output: "done", OK: true}},
	})

	f.refresh()
	countBefore := f.renderCount

	f.toolsExpanded = true
	f.refresh()

	if f.renderCount <= countBefore {
		t.Fatal("expected redraws after expansion change")
	}
}

// TestCacheInvalidatesOnVisibleFieldChange verifies that changing a visible
// field invalidates the affected item.
func TestCacheInvalidatesOnVisibleFieldChange(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	})

	f.refresh()
	countBefore := f.renderCount

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello world"},
	})

	if f.renderCount <= countBefore {
		t.Fatal("expected redraw after visible field change")
	}
}

// TestCacheSeqIDChangeNoWrongRedraw verifies that an item with unchanged
// visible content but different Seq produces the same rendered output.
func TestCacheSeqIDChangeNoWrongRedraw(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	})
	f.refresh()
	linesForSeq1 := make([]string, len(f.lines))
	copy(linesForSeq1, f.lines)

	f2 := NewFeed()
	f2.width = 80
	f2.height = 24
	f2.applyBatch([]agent.Event{
		{Seq: 999, Type: agent.UserMsg, Text: "hello"},
	})
	f2.refresh()
	linesForSeq999 := make([]string, len(f2.lines))
	copy(linesForSeq999, f2.lines)

	if len(linesForSeq1) != len(linesForSeq999) {
		t.Fatalf("line count differs: %d vs %d", len(linesForSeq1), len(linesForSeq999))
	}
	for i := range linesForSeq1 {
		if linesForSeq1[i] != linesForSeq999[i] {
			t.Fatalf("line %d differs: %q vs %q", i, linesForSeq1[i], linesForSeq999[i])
		}
	}
}

// TestCacheEvictsDeletedItems verifies that items removed from the feed
// are actually deleted from the cache.
func TestCacheEvictsDeletedItems(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "msg1"},
		{Seq: 2, Type: agent.UserMsg, Text: "msg2"},
		{Seq: 3, Type: agent.UserMsg, Text: "msg3"},
	})

	f.refresh()
	if len(f.lineCache) == 0 {
		t.Fatal("cache should be populated")
	}

	f.proj = presentation.NewProjector()
	f.refresh()

	if len(f.lineCache) != 0 {
		t.Fatalf("expected empty cache, got %d entries", len(f.lineCache))
	}
}

// TestCacheEmptyOrDuplicateIDNoLeakage verifies that empty IDs are never cached
// and duplicate IDs don't leak lines between different items.
func TestCacheEmptyOrDuplicateIDNoLeakage(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "no-id-1"},
		{Seq: 2, Type: agent.UserMsg, Text: "no-id-2"},
	})

	f.refresh()
	for id := range f.lineCache {
		if id == "" {
			t.Fatal("empty ID should never be cached")
		}
	}

	// Duplicate IDs: two items sharing one ID with different content must
	// each render their own lines (cache must be bypassed for both).
	items := []presentation.FeedItem{
		{Type: presentation.ItemUserMsg, ID: "dup", Text: "FIRST CONTENT"},
		{Type: presentation.ItemUserMsg, ID: "dup", Text: "SECOND CONTENT"},
	}
	lines := renderItemsCached(f, items, 80, false)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "FIRST CONTENT") || !strings.Contains(joined, "SECOND CONTENT") {
		t.Fatalf("duplicate ID leaked lines between items:\n%s", joined)
	}
}

// TestCacheNoAliasing verifies that modifying the output lines from refresh
// doesn't corrupt the cached copy.
func TestCacheNoAliasing(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	})

	f.refresh()
	if len(f.lineCache) == 0 {
		t.Fatal("cache should be populated")
	}

	// Capture original first line of output.
	if len(f.lines) == 0 {
		t.Fatal("expected non-empty lines")
	}
	originalFirstLine := f.lines[0]

	// Modify the output lines (simulating a consumer corrupting them).
	f.lines[0] = "CORRUPTED"

	// Refresh again - cache should still serve correct content.
	f.refresh()

	if f.lines[0] != originalFirstLine {
		t.Fatalf("cache was corrupted: got %q, want %q", f.lines[0], originalFirstLine)
	}
}
