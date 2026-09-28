package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

// TestRefreshIncrementalDirtyTracking verifies the L15 fast path: refreshes
// with no new events report unchanged without re-hashing content, while
// streamed deltas are still detected and rendered.
func TestRefreshIncrementalDirtyTracking(t *testing.T) {
	f := NewFeed()
	f.width, f.height = 80, 24
	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
		{Seq: 2, Type: agent.TextDelta, Text: "wor"},
	})
	// applyBatch renders internally; the first explicit refresh is a no-op.
	if f.refresh() {
		t.Fatal("refresh with no new events must report unchanged")
	}
	before := strings.Join(f.lines, "\n")
	if !strings.Contains(before, "wor") {
		t.Fatalf("streamed text missing:\n%s", before)
	}

	// No new events: provably unchanged.
	if f.refresh() {
		t.Fatal("refresh with no new events must report unchanged")
	}
	if got := strings.Join(f.lines, "\n"); got != before {
		t.Fatal("fast-path refresh altered the lines")
	}
	if f.refresh() {
		t.Fatal("third refresh must also report unchanged")
	}

	// A new delta touches exactly the assistant item. Apply it directly to the
	// projector (applyBatch would render internally).
	if err := f.proj.Apply(agent.Event{Seq: 3, Type: agent.TextDelta, Text: "ld"}); err != nil {
		t.Fatal(err)
	}
	if !f.refresh() {
		t.Fatal("refresh after a text delta must report changed")
	}
	if got := strings.Join(f.lines, "\n"); !strings.Contains(got, "world") {
		t.Fatalf("delta not rendered:\n%s", got)
	}
	if f.refresh() {
		t.Fatal("refresh after rendering the delta must report unchanged")
	}
}

// TestRefreshDetectsExpansionChange ensures per-card expansion toggles are
// never swallowed by the incremental path.
func TestRefreshDetectsExpansionChange(t *testing.T) {
	f := feedWithTools(t, 2, 80) // helper already renders once
	if f.refresh() {
		t.Fatal("refresh with no changes must report unchanged")
	}
	tools := toolIndexes(f)
	if len(tools) == 0 {
		t.Fatal("no tool cards")
	}
	if !f.toggleCard(tools[0]) {
		t.Fatal("toggleCard refused")
	}
	if !f.refresh() {
		t.Fatal("refresh after toggleCard must report changed")
	}
	if f.refresh() {
		t.Fatal("refresh after rendering the toggle must report unchanged")
	}
	// Toggle back: also detected.
	if !f.toggleCard(tools[0]) {
		t.Fatal("toggleCard refused")
	}
	if !f.refresh() {
		t.Fatal("refresh after second toggle must report changed")
	}
}

// TestRefreshDetectsWidthChange ensures a resize takes the full render path
// (line cache invalidated) and reports changed when wrapping actually
// differs.
func TestRefreshDetectsWidthChange(t *testing.T) {
	f := NewFeed()
	f.width, f.height = 80, 24
	long := strings.Repeat("w", 200)
	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: long},
	})
	narrowRows := len(f.lines)
	if narrowRows == 0 {
		t.Fatal("expected rendered lines")
	}
	if f.refresh() {
		t.Fatal("second refresh must report unchanged")
	}
	f.width = 200
	if !f.refresh() {
		t.Fatal("refresh after width change must report changed")
	}
	if f.cacheWidth != 200 {
		t.Fatalf("line cache not invalidated on resize: cacheWidth=%d", f.cacheWidth)
	}
	if len(f.lines) >= narrowRows {
		t.Fatalf("wider viewport should wrap less: %d -> %d rows", narrowRows, len(f.lines))
	}
	if f.refresh() {
		t.Fatal("refresh after rendering the resize must report unchanged")
	}
}

// TestFingerprintCacheStaysBounded ensures evicted items do not pin
// fingerprints (or memory) forever.
func TestFingerprintCacheStaysBounded(t *testing.T) {
	f := NewFeed()
	f.width, f.height = 80, 24
	var evs []agent.Event
	for i := 1; i <= 10; i++ {
		evs = append(evs, agent.Event{Seq: i, Type: agent.UserMsg, Text: "m"})
	}
	f.applyBatch(evs)
	f.refresh()
	if len(f.fpCache) != 10 {
		t.Fatalf("fpCache=%d, want 10", len(f.fpCache))
	}
	// Rebuild from a smaller history: the cache must reset, not accumulate.
	f.BuildFromEvents(evs[:3])
	f.refresh()
	if len(f.fpCache) != 3 {
		t.Fatalf("fpCache=%d after rebuild, want 3", len(f.fpCache))
	}
}
