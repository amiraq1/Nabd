package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderBlocksMatchesLinePipeline verifies the block pipeline is the
// single render path: flattening renderBlocks reproduces the old flat line
// output byte-for-byte, including separators and hard-wrapping.
func TestRenderBlocksMatchesLinePipeline(t *testing.T) {
	items := []presentation.FeedItem{
		{Type: presentation.ItemUserMsg, Text: "user question here"},
		{Type: presentation.ItemAssistant, Text: "assistant answer"},
		{Type: presentation.ItemTool, Tool: &presentation.ToolCard{Name: "bash", Status: presentation.ToolDone, Args: "ls -la"}},
		{Type: presentation.ItemNotice, Text: "a note"},
		{Type: presentation.ItemUserMsg, Text: "second question"},
	}
	for _, width := range []int{10, 20, 40} {
		lines, offsets := renderItemsWithOffsets(items, width, false)
		blocks := renderBlocks(items, width, false)
		flat, flatOffsets := flattenBlocks(blocks)
		if strings.Join(flat, "\n") != strings.Join(lines, "\n") {
			t.Fatalf("width %d: block flatten != line pipeline:\nblocks:\n%s\nlines:\n%s", width, strings.Join(flat, "\n"), strings.Join(lines, "\n"))
		}
		if len(flatOffsets) != len(offsets) {
			t.Fatalf("width %d: offset count %d != %d", width, len(flatOffsets), len(offsets))
		}
		for i := range offsets {
			if flatOffsets[i] != offsets[i] {
				t.Fatalf("width %d: offset[%d] = %d, want %d", width, i, flatOffsets[i], offsets[i])
			}
		}
		// Every stored line must fit within the terminal width.
		for _, l := range flat {
			if w := ansi.StringWidth(l); w > width {
				t.Fatalf("width %d: stored line exceeds width (%d): %q", width, w, l)
			}
		}
		// Each block is bound to its source item (Batch 2 fingerprint unit).
		if len(blocks) != len(items) {
			t.Fatalf("width %d: block count %d != item count %d", width, len(blocks), len(items))
		}
		for i := range blocks {
			if blocks[i].Item.Type != items[i].Type {
				t.Fatalf("width %d: block %d bound to item type %s, want %s", width, i, blocks[i].Item.Type, items[i].Type)
			}
		}
	}
}

// TestMultiLineSlashOutputRoutesToFeedBlock verifies that multi-line slash
// output (e.g. /edits with several pending edits) goes into the feed as a
// notice block instead of being flattened onto the one-line status row.
func TestMultiLineSlashOutputRoutesToFeedBlock(t *testing.T) {
	f := NewFeed()
	f.width = 60
	f.height = 20
	f.SetCallbacks(&SessionCallbacks{
		OnEdits: func() string {
			return "1· edit_file internal/a.go\n2· write_file internal/b.go"
		},
	})
	_, _ = f.runCommand("/edits")

	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1", len(f.notices))
	}
	if f.notices[0].Type != presentation.ItemNotice {
		t.Fatalf("notice type = %s, want ItemNotice", f.notices[0].Type)
	}
	if f.status != "" {
		t.Fatalf("status = %q, want empty (multi-line output must not flatten into the status row)", f.status)
	}
	joined := strings.Join(f.lines, "\n")
	if !strings.Contains(joined, "1· edit_file internal/a.go") || !strings.Contains(joined, "2· write_file internal/b.go") {
		t.Fatalf("multi-line output missing from feed lines:\n%s", joined)
	}
	// Both lines must be present as separate feed rows.
	if got := strings.Count(joined, "edit_file internal/a.go"); got != 1 {
		t.Fatalf("first output line appears %d times, want 1:\n%s", got, joined)
	}
}

// TestSingleLineSlashOutputStaysInStatus verifies single-line slash results
// still use the transient status row and never become feed blocks.
func TestSingleLineSlashOutputStaysInStatus(t *testing.T) {
	f := NewFeed()
	f.SetCallbacks(&SessionCallbacks{
		OnCtx:     func() string { return "context 42%" },
		OnEdits:   func() string { return "1· edit_file internal/a.go" },
		OnCompact: func() string { return "compacting in background" },
	})

	_, _ = f.runCommand("/ctx")
	if f.status != "context 42%" {
		t.Fatalf("/ctx status = %q, want %q", f.status, "context 42%")
	}
	if len(f.notices) != 0 {
		t.Fatalf("/ctx added %d notices, want 0", len(f.notices))
	}

	_, _ = f.runCommand("/edits")
	if f.status != "1· edit_file internal/a.go" {
		t.Fatalf("/edits status = %q, want single line preserved", f.status)
	}
	if len(f.notices) != 0 {
		t.Fatalf("/edits added %d notices, want 0", len(f.notices))
	}

	_, _ = f.runCommand("/compact")
	if f.status != "compacting in background" {
		t.Fatalf("/compact status = %q, want %q", f.status, "compacting in background")
	}
	if len(f.notices) != 0 {
		t.Fatalf("/compact added %d notices, want 0", len(f.notices))
	}
}

// TestRenderNoticeMultiLine verifies renderNotice preserves line structure
// with the badge on the first line only.
func TestRenderNoticeMultiLine(t *testing.T) {
	it := presentation.FeedItem{
		Type: presentation.ItemNotice,
		Text: "1· edit_file a.go\n2· write_file b.go",
	}
	got := renderNotice(it, 40)
	if len(got) != 2 {
		t.Fatalf("renderNotice lines = %d, want 2: %q", len(got), got)
	}
	if !strings.Contains(got[0], "⚑") {
		t.Fatalf("first line missing badge: %q", got[0])
	}
	if strings.Contains(got[1], "⚑") {
		t.Fatalf("continuation line must not repeat the badge: %q", got[1])
	}
	if !strings.Contains(got[1], "2· write_file b.go") {
		t.Fatalf("continuation line content missing: %q", got[1])
	}
}

// liveStreamingFeed builds a feed with 20 items (19 user messages + 1 running tool)
// and streaming timestamps initialized.
// Time markers are anchored in the past so every delta is already elapsed on
// every render, regardless of when the package starts running. A live
// time.Now() would put the first ~2.24s of deltas in the future (then flip to
// the past), making both the rendered content and the allocation count vary
// with wall-clock timing. A guard must not be able to fail because the machine
// was slow.
func liveStreamingFeed(t testing.TB) *Feed {
	t.Helper()
	f := NewFeed()
	f.width, f.height = 120, 24
	f.running, f.busy = true, true

	// Anchor in the past so both deltas are already elapsed on every render.
	start := time.Now().Add(-10 * time.Second)
	f.reqStartedAt, f.streamStartedAt = start, start
	f.streamFirstDeltaAt = start.Add(1240 * time.Millisecond)
	f.streamLastDeltaAt = start.Add(2240 * time.Millisecond)
	f.streamedChars = 180

	for i := 1; i <= 19; i++ {
		_ = f.proj.Apply(agent.Event{
			Seq:  i,
			Type: agent.UserMsg,
			Text: fmt.Sprintf("message %d", i),
		})
	}
	_ = f.proj.Apply(agent.Event{
		Seq:  20,
		Type: agent.ToolStart,
		Call: &agent.ToolCall{ID: "c1", Name: "bash"},
	})
	f.refresh()
	return f
}
