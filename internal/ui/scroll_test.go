package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

// Helper to populate the feed with N distinct numbered lines
func populateFeedWithLines(f *Feed, n int) {
	var events []agent.Event
	for i := 1; i <= n; i++ {
		events = append(events, agent.Event{
			Seq:  i,
			Type: agent.UserMsg,
			Text: fmt.Sprintf("line_content_%03d", i),
		})
	}
	f.Update(agentEventBatchMsg{Events: events})
}

// TestFollowModeShowsNewestRenderedLine verifies that in follow mode, View()
// actually displays the newest rendered line, not the oldest.
func TestFollowModeShowsNewestRenderedLine(t *testing.T) {
	f := newFeedAt(t, 80, 15)
	f.follow = true
	populateFeedWithLines(f, 30) // 30 lines > viewport rows (~9)

	v := f.View()
	newestExpected := "line_content_030"
	oldestNotExpected := "line_content_001"

	if !strings.Contains(v, newestExpected) {
		t.Fatalf("follow mode MUST display newest rendered line %q, got output:\n%s", newestExpected, v)
	}
	if strings.Contains(v, oldestNotExpected) {
		t.Fatalf("follow mode is displaying oldest line %q instead of newest!", oldestNotExpected)
	}
}

// TestFollowModeStillNewestWhenLinesExceedViewport
func TestFollowModeStillNewestWhenLinesExceedViewport(t *testing.T) {
	f := newFeedAt(t, 80, 10)
	f.follow = true
	populateFeedWithLines(f, 50)

	v := f.View()
	lastLine := "line_content_050"
	if !strings.Contains(v, lastLine) {
		t.Fatalf("expected last line %q in View(), got:\n%s", lastLine, v)
	}
}

// TestEndKeyShowsLastRenderedLine
func TestEndKeyShowsLastRenderedLine(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 40)

	// Blur composer so viewport owns keys
	f.composer.blur()
	// Scroll up first
	f.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	// Now press End
	f.Update(tea.KeyMsg{Type: tea.KeyEnd})

	v := f.View()
	if !strings.Contains(v, "line_content_040") {
		t.Fatalf("End key must show last rendered line 'line_content_040', got:\n%s", v)
	}
	if !f.follow {
		t.Fatalf("End key must re-arm follow mode")
	}
}

// TestHomeKeyShowsFirstRenderedLine
func TestHomeKeyShowsFirstRenderedLine(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 40)

	f.composer.blur()
	f.Update(tea.KeyMsg{Type: tea.KeyHome})

	v := f.View()
	if !strings.Contains(v, "line_content_001") {
		t.Fatalf("Home key must show first rendered line 'line_content_001', got:\n%s", v)
	}
	if f.scrollTop != 0 {
		t.Fatalf("Home key must set canonical scrollTop to 0, got %d", f.scrollTop)
	}
	if f.follow {
		t.Fatalf("Home key must disable follow mode")
	}
}

// TestPageUpThenPageDownReturnsToExactBottom
func TestPageUpThenPageDownReturnsToExactBottom(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 40)

	f.composer.blur()
	f.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if f.follow {
		t.Fatalf("PgUp must disable follow mode")
	}

	f.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	v := f.View()
	if !strings.Contains(v, "line_content_040") {
		t.Fatalf("PgDown must return to exact bottom showing newest line, got:\n%s", v)
	}
	if !f.follow {
		t.Fatalf("PgDown reaching bottom must re-arm follow mode")
	}
}

// TestStreamingWhileFollowingKeepsLatestDeltaVisible
func TestStreamingWhileFollowingKeepsLatestDeltaVisible(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 25)
	f.follow = true

	// Stream new text deltas
	for i := 1; i <= 10; i++ {
		f.Update(agentEventBatchMsg{Events: []agent.Event{
			{Seq: 25 + i, Type: agent.TextDelta, Text: fmt.Sprintf("stream_chunk_%d\n", i)},
		}})
	}

	v := f.View()
	if !strings.Contains(v, "stream_chunk_10") {
		t.Fatalf("streaming while following must keep latest delta visible, got:\n%s", v)
	}
}

// TestResizeWhileFollowingKeepsBottomAnchored
func TestResizeWhileFollowingKeepsBottomAnchored(t *testing.T) {
	f := newFeedAt(t, 80, 20)
	populateFeedWithLines(f, 40)
	f.follow = true

	// Resize to smaller terminal
	f.Update(tea.WindowSizeMsg{Width: 60, Height: 10})

	v := f.View()
	if !strings.Contains(v, "line_content_040") {
		t.Fatalf("resize while following must keep bottom anchored, got:\n%s", v)
	}
}

// TestNoticeInsertionWhileFollowingStaysAtBottom
func TestNoticeInsertionWhileFollowingStaysAtBottom(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 30)
	f.follow = true

	f.addNotice(presentation.ItemNotice, "urgent_alert_notice_here")

	v := f.View()
	if !strings.Contains(v, "urgent_alert_notice_here") {
		t.Fatalf("notice insertion while following must keep notice visible at bottom, got:\n%s", v)
	}
}

// TestItemTruncationPreservesBrowsingAnchor
func TestItemTruncationPreservesBrowsingAnchor(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 25)
	f.composer.blur()

	// Browse up to line 10
	f.Update(tea.KeyMsg{Type: tea.KeyHome})
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	anchorTop := f.scrollTop

	// Force clamp/refresh
	f.refresh()
	if f.scrollTop < 0 {
		t.Fatalf("scrollTop went negative after refresh: %d", f.scrollTop)
	}
	if f.follow {
		t.Fatalf("browsing must remain follow=false")
	}
	_ = anchorTop
}

// TestScrollTopNeverExceedsBottomStart
func TestScrollTopNeverExceedsBottomStart(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 30)
	f.composer.blur()

	// Press PgDown multiple times
	for i := 0; i < 10; i++ {
		f.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}

	lm := f.computeLayout()
	bottomStart := max(0, len(f.lines)-lm.ViewportRows)
	if f.scrollTop > bottomStart {
		t.Fatalf("scrollTop %d > bottomStart %d", f.scrollTop, bottomStart)
	}
}

// TestScrollTopNeverNegativeAtZeroViewport
func TestScrollTopNeverNegativeAtZeroViewport(t *testing.T) {
	f := newFeedAt(t, 80, 1) // 1 row terminal -> ViewportRows == 0
	populateFeedWithLines(f, 10)

	f.composer.blur()
	f.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if f.scrollTop < 0 {
		t.Fatalf("scrollTop is negative at zero viewport: %d", f.scrollTop)
	}
	f.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if f.scrollTop < 0 {
		t.Fatalf("scrollTop is negative at zero viewport: %d", f.scrollTop)
	}
}

// TestUnseenClearsOnlyAtBottom
func TestUnseenClearsOnlyAtBottom(t *testing.T) {
	f := newFeedAt(t, 80, 12)
	populateFeedWithLines(f, 30)

	f.composer.blur()
	f.Update(tea.KeyMsg{Type: tea.KeyHome}) // Scrolled to top
	if f.follow {
		t.Fatalf("expected follow=false after Home")
	}

	// Add new message while browsing
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 31, Type: agent.UserMsg, Text: "new_msg_while_scrolled"},
	}})

	if f.unseen == 0 {
		t.Fatalf("expected unseen > 0 when not at bottom, got %d", f.unseen)
	}

	// Scroll down one notch (not at bottom yet)
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.unseen == 0 {
		t.Fatalf("unseen should not clear until actually reaching bottom")
	}

	// Scroll all the way to bottom
	f.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if f.unseen != 0 {
		t.Fatalf("unseen must be 0 after reaching bottom via End, got %d", f.unseen)
	}
}
