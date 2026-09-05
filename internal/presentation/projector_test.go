package presentation_test

import (
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"
	"nabd/internal/store"
)

// TestBuildEmpty verifies an empty journal produces an empty feed.
func TestBuildEmpty(t *testing.T) {
	p := presentation.NewProjector()
	items, err := p.Build(nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

// TestBuildSingleUserMsg verifies a single user message.
func TestBuildSingleUserMsg(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "start"},
		{Seq: 2, Type: agent.UserMsg, Text: "hi"},
		{Seq: 3, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(items) != 2 { // run_boundary + user_msg
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}
	if items[0].Type != presentation.ItemRunBoundary {
		t.Errorf("item[0].Type = %v, want ItemRunBoundary", items[0].Type)
	}
	if items[1].Type != presentation.ItemUserMsg || items[1].Text != "hi" {
		t.Errorf("item[1] = %+v, want user_msg 'hi'", items[1])
	}
}

// TestBuildAssistantNonStreamed verifies a complete assistant message.
func TestBuildAssistantNonStreamed(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.UserMsg, Text: "q"},
		{Seq: 3, Type: agent.TextDelta, Text: "hello world"},
		{Seq: 4, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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
	if asst.Text != "hello world" {
		t.Errorf("assistant text = %q, want 'hello world'", asst.Text)
	}
}

// TestBuildMultipleTextDeltasMerge verifies several deltas collapse into one.
func TestBuildMultipleTextDeltasMerge(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.UserMsg, Text: "q"},
		{Seq: 3, Type: agent.TextDelta, Text: "Hello "},
		{Seq: 4, Type: agent.TextDelta, Text: "world"},
		{Seq: 5, Type: agent.TextDelta, Text: "!"},
		{Seq: 6, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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

// TestBuildTwoSeparateMessagesDoNotMerge verifies distinct turns stay separate.
func TestBuildTwoSeparateMessagesDoNotMerge(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.UserMsg, Text: "first"},
		{Seq: 3, Type: agent.TextDelta, Text: "reply one"},
		{Seq: 4, Type: agent.TurnEnd},
		{Seq: 5, Type: agent.UserMsg, Text: "second"},
		{Seq: 6, Type: agent.TextDelta, Text: "reply two"},
		{Seq: 7, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var asstItems []*presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemAssistant {
			asstItems = append(asstItems, &items[i])
		}
	}
	if len(asstItems) != 2 {
		t.Fatalf("expected 2 assistant items, got %d", len(asstItems))
	}
	if asstItems[0].Text != "reply one" {
		t.Errorf("asst[0].Text = %q, want 'reply one'", asstItems[0].Text)
	}
	if asstItems[1].Text != "reply two" {
		t.Errorf("asst[1].Text = %q, want 'reply two'", asstItems[1].Text)
	}
}

// TestBuildToolLifecycle verifies tool start → end transitions.
func TestBuildToolLifecycle(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "read_file", Args: []byte(`{"path":"a.go"}`)}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "read_file", Output: "content", OK: true}},
		{Seq: 4, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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
	if tool.Tool.Output != "content" {
		t.Errorf("tool output = %q, want 'content'", tool.Tool.Output)
	}
	if tool.Tool.Name != "read_file" {
		t.Errorf("tool name = %q, want 'read_file'", tool.Tool.Name)
	}
}

// TestBuildToolFailure verifies failed tool status.
func TestBuildToolFailure(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash", Args: []byte(`{"cmd":"bad"}`)}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "bash", OK: false, Exit: 1}},
		{Seq: 4, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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
	if tool.Tool.Status != presentation.ToolFailed {
		t.Errorf("tool status = %v, want ToolFailed", tool.Tool.Status)
	}
}

// TestBuildPermissionAllow verifies permission request → allow.
func TestBuildPermissionAllow(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash", Args: []byte(`{"cmd":"ls"}`)}},
		{Seq: 3, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.AllowOnce, RawDecision: agent.AllowOnce},
		{Seq: 4, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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

// TestBuildPermissionDeny verifies permission request → deny.
func TestBuildPermissionDeny(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
		{Seq: 3, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.Deny, RawDecision: agent.Deny},
		{Seq: 4, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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

// TestBuildNoticeAndError verifies notice and error items.
func TestBuildNoticeAndError(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.Notice, Text: "calibrated"},
		{Seq: 3, Type: agent.RunError, Err: "boom"},
		{Seq: 4, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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

// TestBuildUnknownEventNoPanic verifies unknown events are skipped safely.
func TestBuildUnknownEventNoPanic(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.EventType("future_event"), Text: "unknown"},
		{Seq: 3, Type: agent.UserMsg, Text: "hi"},
		{Seq: 4, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Unknown event should be skipped; we still get run_boundary + user_msg.
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d: %+v", len(items), items)
	}
}

// TestBuildVsApplyEquivalence verifies Build(all) == sequential Apply.
func TestBuildVsApplyEquivalence(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "start"},
		{Seq: 2, Type: agent.UserMsg, Text: "hi"},
		{Seq: 3, Type: agent.TextDelta, Text: "Hello "},
		{Seq: 4, Type: agent.TextDelta, Text: "there"},
		{Seq: 5, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "read_file", Args: []byte(`{"path":"a.go"}`)}},
		{Seq: 6, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "read_file", Output: "ok", OK: true}},
		{Seq: 7, Type: agent.TurnEnd},
		{Seq: 8, Type: agent.RunEnd, Text: "done"},
	}

	p1 := presentation.NewProjector()
	built, err := p1.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p2 := presentation.NewProjector()
	for _, e := range evs {
		if err := p2.Apply(e); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	applied := p2.Items()

	if len(built) != len(applied) {
		t.Fatalf("Build produced %d items, Apply produced %d", len(built), len(applied))
	}
	for i := range built {
		if built[i].Type != applied[i].Type || built[i].ID != applied[i].ID || built[i].Text != applied[i].Text {
			t.Errorf("item[%d] mismatch:\n  built:  %+v\n  applied: %+v", i, built[i], applied[i])
		}
	}
}

// TestItemsImmutable verifies callers cannot mutate internal state via Items().
func TestItemsImmutable(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.UserMsg, Text: "hi"},
		{Seq: 3, Type: agent.TurnEnd},
	}
	p := presentation.NewProjector()
	if _, err := p.Build(evs); err != nil {
		t.Fatalf("Build: %v", err)
	}
	items := p.Items()
	if len(items) == 0 {
		t.Fatal("no items")
	}
	// Mutate the returned slice.
	items[0].Text = "MUTATED"
	// Internal state must be unchanged.
	fresh := p.Items()
	if fresh[0].Text == "MUTATED" {
		t.Error("Items() returned a reference to internal state; mutation leaked")
	}
}

// TestReplayFromFixture replays the bundled session fixture.
func TestReplayFromFixture(t *testing.T) {
	evs, err := store.Read("../../testdata/session.jsonl")
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	p := presentation.NewProjector()
	items, err := p.Build(evs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(items) == 0 {
		t.Error("fixture produced no items")
	}
	// Verify ordering is stable: Seq should be non-decreasing for items that
	// have it set.
	var lastSeq int
	for _, it := range items {
		if it.Seq != 0 && it.Seq < lastSeq {
			t.Errorf("items not in stable order: seq %d after %d", it.Seq, lastSeq)
		}
		if it.Seq != 0 {
			lastSeq = it.Seq
		}
	}
}

// TestSliceReallocationStrandsAssistantText verifies that appending items
// during streaming deltas does not strand assistant text due to stale slice pointers.
func TestSliceReallocationStrandsAssistantText(t *testing.T) {
	p := presentation.NewProjector()
	_ = p.Apply(agent.Event{Seq: 1, Type: agent.TextDelta, Text: "part1 "})
	// Force slice reallocation by appending many notices
	for i := 0; i < 100; i++ {
		_ = p.Apply(agent.Event{Seq: 10 + i, Type: agent.Notice, Text: "n"})
	}
	// Append another delta to the same assistant message
	_ = p.Apply(agent.Event{Seq: 200, Type: agent.TextDelta, Text: "part2"})
	_ = p.Apply(agent.Event{Seq: 201, Type: agent.TurnEnd})

	items := p.Items()
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
	if asst.Text != "part1 part2" {
		t.Fatalf("expected 'part1 part2', got %q", asst.Text)
	}
}

// TestSliceReallocationToolEnd verifies that ToolEnd updates the correct item
// even after many intervening events force slice reallocations.
func TestSliceReallocationToolEnd(t *testing.T) {
	p := presentation.NewProjector()
	_ = p.Apply(agent.Event{
		Seq:  1,
		Type: agent.ToolStart,
		Call: &agent.ToolCall{ID: "call_realloc_1", Name: "bash", Args: []byte(`{"cmd":"ls"}`)},
	})
	// Force slice reallocation
	for i := 0; i < 150; i++ {
		_ = p.Apply(agent.Event{Seq: 10 + i, Type: agent.Notice, Text: "n"})
	}
	// ToolEnd for the initial tool call
	_ = p.Apply(agent.Event{
		Seq:  300,
		Type: agent.ToolEnd,
		Call: &agent.ToolCall{ID: "call_realloc_1", Name: "bash", Output: "done_ls", OK: true},
	})

	items := p.Items()
	var tool *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemTool && items[i].ID == "tool_call_realloc_1" {
			tool = &items[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("tool item not found")
	}
	if tool.Tool.Status != presentation.ToolDone {
		t.Fatalf("expected ToolDone, got %v", tool.Tool.Status)
	}
	if tool.Tool.Output != "done_ls" {
		t.Fatalf("expected 'done_ls', got %q", tool.Tool.Output)
	}
}

// TestSliceReallocationPermReply verifies that PermReply updates the correct item
// even after many intervening events force slice reallocations.
func TestSliceReallocationPermReply(t *testing.T) {
	p := presentation.NewProjector()
	_ = p.Apply(agent.Event{
		Seq:  1,
		Type: agent.PermAsk,
		Call: &agent.ToolCall{ID: "perm_realloc_1", Name: "bash", Args: []byte(`{"cmd":"rm"}`)},
	})
	// Force slice reallocation
	for i := 0; i < 150; i++ {
		_ = p.Apply(agent.Event{Seq: 10 + i, Type: agent.Notice, Text: "n"})
	}
	// PermReply
	_ = p.Apply(agent.Event{
		Seq:         300,
		Type:        agent.PermReply,
		Call:        &agent.ToolCall{ID: "perm_realloc_1", Name: "bash"},
		Decision:    agent.AllowOnce,
		RawDecision: agent.AllowOnce,
	})

	items := p.Items()
	var perm *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemPermission && items[i].ID == "tool_perm_realloc_1" {
			perm = &items[i]
			break
		}
	}
	if perm == nil {
		t.Fatal("perm item not found")
	}
	if perm.Perm.Status != presentation.PermAllow {
		t.Fatalf("expected PermAllow, got %v", perm.Perm.Status)
	}
}

// TestItemsDeepCopyToolAndPerm verifies that mutating ToolCard or PermCard
// returned by Items() does not mutate the projector's internal state.
func TestItemsDeepCopyToolAndPerm(t *testing.T) {
	p := presentation.NewProjector()
	_ = p.Apply(agent.Event{
		Seq:  1,
		Type: agent.ToolStart,
		Call: &agent.ToolCall{ID: "c1", Name: "bash"},
	})
	_ = p.Apply(agent.Event{
		Seq:  2,
		Type: agent.PermAsk,
		Call: &agent.ToolCall{ID: "c2", Name: "bash"},
	})
	items := p.Items()
	for i := range items {
		if items[i].Tool != nil {
			items[i].Tool.Status = presentation.ToolFailed
		}
		if items[i].Perm != nil {
			items[i].Perm.Status = presentation.PermDeny
		}
	}
	fresh := p.Items()
	for _, it := range fresh {
		if it.Tool != nil && it.Tool.Status == presentation.ToolFailed {
			t.Errorf("ToolCard mutation leaked into projector internal state")
		}
		if it.Perm != nil && it.Perm.Status == presentation.PermDeny {
			t.Errorf("PermCard mutation leaked into projector internal state")
		}
	}
}

// TestSortBySeqStableOrder verifies that items with identical Seq preserve their
// insertion order rather than being sorted by arbitrary ID.
func TestSortBySeqStableOrder(t *testing.T) {
	p := presentation.NewProjector()
	// Insert item with "z_id" first, then "a_id", with same Seq
	_ = p.Apply(agent.Event{Seq: 10, Type: agent.Notice, Text: "first_inserted"})
	_ = p.Apply(agent.Event{Seq: 10, Type: agent.Notice, Text: "second_inserted"})

	items := p.Items()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Text != "first_inserted" || items[1].Text != "second_inserted" {
		t.Fatalf("stable order violated: items[0]=%q, items[1]=%q", items[0].Text, items[1].Text)
	}
}

// TestIsTruncatedRealMarker verifies that the real store marker is detected.
func TestIsTruncatedRealMarker(t *testing.T) {
	p := presentation.NewProjector()
	output := "some output\n...[truncated 100 bytes]"
	_ = p.Apply(agent.Event{
		Seq:  1,
		Type: agent.ToolStart,
		Call: &agent.ToolCall{ID: "c_trunc", Name: "bash"},
	})
	_ = p.Apply(agent.Event{
		Seq:  2,
		Type: agent.ToolEnd,
		Call: &agent.ToolCall{ID: "c_trunc", Name: "bash", Output: output, OK: true},
	})
	items := p.Items()
	var tool *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemTool && items[i].ID == "tool_c_trunc" {
			tool = &items[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("tool not found")
	}
	if !tool.Tool.Truncated {
		t.Errorf("expected tool.Truncated to be true for real store marker %q", output)
	}
}

// TestCallErrMeaningfulMessage verifies that tool failure populates Tool.Err with a meaningful message.
func TestCallErrMeaningfulMessage(t *testing.T) {
	p := presentation.NewProjector()
	_ = p.Apply(agent.Event{
		Seq:  1,
		Type: agent.ToolStart,
		Call: &agent.ToolCall{ID: "c_err", Name: "bash"},
	})
	_ = p.Apply(agent.Event{
		Seq:  2,
		Type: agent.ToolEnd,
		Call: &agent.ToolCall{ID: "c_err", Name: "bash", Output: "command not found: abc\nexit status 127", OK: false, Exit: 127},
	})
	items := p.Items()
	var tool *presentation.FeedItem
	for i := range items {
		if items[i].Type == presentation.ItemTool && items[i].ID == "tool_c_err" {
			tool = &items[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("tool not found")
	}
	if tool.Tool.Err != "command not found: abc" {
		t.Errorf("tool.Err = %q, want 'command not found: abc'", tool.Tool.Err)
	}
}

// TestEventProviderRouteDeferredToU2 verifies that router decision events
// are ignored by the presentation projector without creating items or incrementing unhandled events.
func TestEventProviderRouteDeferredToU2(t *testing.T) {
	p := presentation.NewProjector()
	err := p.Apply(agent.Event{
		Seq:  1,
		Type: agent.EventProviderRoute,
		Text: "router selected provider",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Items()) != 0 {
		t.Errorf("expected 0 items, got %d", len(p.Items()))
	}
	if p.UnhandledEventTypes != nil && p.UnhandledEventTypes[agent.EventProviderRoute] > 0 {
		t.Errorf("EventProviderRoute should not be counted as unhandled")
	}
}

// TestUnhandledEventTypesCounter verifies that unknown event types are tracked in UnhandledEventTypes.
func TestUnhandledEventTypesCounter(t *testing.T) {
	p := presentation.NewProjector()
	unk := agent.EventType("future_unknown_event")
	err := p.Apply(agent.Event{
		Seq:  1,
		Type: unk,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.UnhandledEventTypes == nil || p.UnhandledEventTypes[unk] != 1 {
		t.Errorf("expected UnhandledEventTypes[%q] == 1, got %v", unk, p.UnhandledEventTypes)
	}
}
