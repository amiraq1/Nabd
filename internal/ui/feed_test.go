package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFeedEmpty verifies an empty feed renders without panic.
func TestFeedEmpty(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_ = f.View()
}

// TestFeedUserMsg verifies a user message renders.
func TestFeedUserMsg(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "مرحبا"},
	}})
	view := f.View()
	if !strings.Contains(view, "مرحبا") {
		t.Errorf("view does not contain user message: %q", view)
	}
}

// TestFeedAssistantNonStreamed verifies a complete assistant message.
func TestFeedAssistantNonStreamed(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.TextDelta, Text: "Hello world"},
		{Seq: 2, Type: agent.TurnEnd},
	}})
	items := f.proj.Items()
	var found bool
	for _, it := range items {
		if it.Type == presentation.ItemAssistant && it.Text == "Hello world" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected assistant item with text 'Hello world', got %+v", items)
	}
}

// TestFeedMultipleDeltasMerge verifies several deltas collapse into one item.
func TestFeedMultipleDeltasMerge(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.TextDelta, Text: "Hello "},
		{Seq: 2, Type: agent.TextDelta, Text: "world"},
		{Seq: 3, Type: agent.TextDelta, Text: "!"},
		{Seq: 4, Type: agent.TurnEnd},
	}})
	items := f.proj.Items()
	var asst *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemAssistant {
			asst = &items[i]
			break
		}
	}
	if asst == nil {
		t.Fatal("no assistant item found")
	}
	if asst.Text != "Hello world!" {
		t.Errorf("assistant text = %q, want 'Hello world!'", asst.Text)
	}
}

// TestFeedToolLifecycle verifies tool start → end.
func TestFeedToolLifecycle(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "read_file", Args: []byte(`{"path":"a.go"}`)}},
		{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "read_file", Output: "content", OK: true}},
	}})
	items := f.proj.Items()
	var tool *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemTool {
			tool = &items[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("no tool item found")
	}
	if tool.Tool.Status != presentation.ToolDone {
		t.Errorf("tool status = %v, want ToolDone", tool.Tool.Status)
	}
}

// TestFeedPermissionAllow verifies permission request → allow.
func TestFeedPermissionAllow(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash", Args: []byte(`{"cmd":"ls"}`)}},
		{Seq: 2, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.AllowOnce, EffectiveDecision: agent.AllowOnce},
	}})
	items := f.proj.Items()
	var perm *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemPermission {
			perm = &items[i]
			break
		}
	}
	if perm == nil {
		t.Fatal("no permission item found")
	}
	if perm.Perm.Status != presentation.PermAllow {
		t.Errorf("perm status = %v, want PermAllow", perm.Perm.Status)
	}
}

// TestFeedPermissionDeny verifies permission request → deny.
func TestFeedPermissionDeny(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
		{Seq: 2, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.Deny, EffectiveDecision: agent.Deny},
	}})
	items := f.proj.Items()
	var perm *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemPermission {
			perm = &items[i]
			break
		}
	}
	if perm == nil {
		t.Fatal("no permission item found")
	}
	if perm.Perm.Status != presentation.PermDeny {
		t.Errorf("perm status = %v, want PermDeny", perm.Perm.Status)
	}
}

// TestFeedEffectiveDecisionDiffers verifies AllowSession → AllowOnce is shown.
func TestFeedEffectiveDecisionDiffers(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
		{Seq: 2, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.AllowSession, EffectiveDecision: agent.AllowOnce},
	}})
	items := f.proj.Items()
	var perm *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemPermission {
			perm = &items[i]
			break
		}
	}
	if perm == nil {
		t.Fatal("no permission item found")
	}
	if perm.Perm.Decision != agent.AllowSession || perm.Perm.Effective != agent.AllowOnce {
		t.Errorf("perm decision/effective = %v/%v, want AllowSession/AllowOnce",
			perm.Perm.Decision, perm.Perm.Effective)
	}
}

// TestFeedNoticeAndError verifies notice and error items.
func TestFeedNoticeAndError(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.Notice, Text: "calibrated"},
		{Seq: 2, Type: agent.RunError, Err: "boom"},
	}})
	items := f.proj.Items()
	var notice, errMsg *presentation.FeedItem
	for i := range items {
		switch items[i].Type {
		case presentation.ItemNotice:
			notice = &items[i]
		case presentation.ItemError:
			errMsg = &items[i]
		}
	}
	if notice == nil || notice.Text != "calibrated" {
		t.Errorf("notice = %+v, want 'calibrated'", notice)
	}
	if errMsg == nil || errMsg.Text != "boom" {
		t.Errorf("error = %+v, want 'boom'", errMsg)
	}
}

// TestFeedUnknownEventNoPanic verifies unknown events are skipped safely.
func TestFeedUnknownEventNoPanic(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	// Should not panic.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.EventType("future_event"), Text: "unknown"},
		{Seq: 2, Type: agent.UserMsg, Text: "hi"},
	}})
	items := f.proj.Items()
	// Unknown event skipped; user_msg present.
	var found bool
	for _, it := range items {
		if it.Type == presentation.ItemUserMsg && it.Text == "hi" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("user_msg not found after unknown event: %+v", items)
	}
}

// TestFeedFollowMode verifies follow mode keeps viewport at bottom.
func TestFeedFollowMode(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	f.follow = true
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "msg1"},
		{Seq: 2, Type: agent.UserMsg, Text: "msg2"},
		{Seq: 3, Type: agent.UserMsg, Text: "msg3"},
	}})
	if !f.follow {
		t.Error("follow mode should remain active when appending at bottom")
	}
	if f.scrollTop != 0 {
		t.Errorf("scrollTop = %d, want 0 (at bottom)", f.scrollTop)
	}
}

// TestFeedScrollUpDisablesFollow verifies scrolling up pauses follow.
func TestFeedScrollUpDisablesFollow(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	f.follow = true
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "msg1"},
		{Seq: 2, Type: agent.UserMsg, Text: "msg2"},
	}})
	// Scroll up.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if f.follow {
		t.Error("follow mode should be disabled after scrolling up")
	}
}

// TestFeedUnseenCounter verifies unseen updates are counted when not following.
func TestFeedUnseenCounter(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	f.follow = true
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "msg1"},
	}})
	// Scroll up to disable follow.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	// New event arrives while not following.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.UserMsg, Text: "msg2"},
	}})
	if f.unseen == 0 {
		t.Errorf("unseen = %d, want > 0", f.unseen)
	}
}

// TestFeedResizeNoPanic verifies resize with extreme dimensions doesn't panic.
func TestFeedResizeNoPanic(t *testing.T) {
	f := NewFeed()
	// Zero dimensions.
	_, _ = f.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	// Tiny dimensions.
	_, _ = f.Update(tea.WindowSizeMsg{Width: 5, Height: 3})
	// Large dimensions.
	_, _ = f.Update(tea.WindowSizeMsg{Width: 200, Height: 100})
	_ = f.View()
}

// TestFeedItemsAreCopy verifies Items() returns a copy.
func TestFeedItemsAreCopy(t *testing.T) {
	f := NewFeed()
	f.width = 50
	f.height = 10
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hi"},
	}})
	// Access internal items via the projector.
	items := f.proj.Items()
	if len(items) == 0 {
		t.Fatal("no items")
	}
	items[0].Text = "MUTATED"
	// Internal state must be unchanged.
	fresh := f.proj.Items()
	if fresh[0].Text == "MUTATED" {
		t.Error("Items() returned a reference to internal state")
	}
}

// TestFeedWideViewportUsesFullWidth verifies the viewport is NOT capped at 60
// columns — a wide terminal should produce a wide feed.
func TestFeedWideViewportUsesFullWidth(t *testing.T) {
	f := NewFeed()
	// Simulate a 120-column terminal.
	_, _ = f.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	if f.width < 120 {
		t.Errorf("width = %d, want >= 120 (viewport should use available width)", f.width)
	}
	// Narrow terminal clamps to minimum.
	f2 := NewFeed()
	_, _ = f2.Update(tea.WindowSizeMsg{Width: 5, Height: 10})
	if f2.width < minViewportWidth {
		t.Errorf("narrow width = %d, want >= %d", f2.width, minViewportWidth)
	}
}
