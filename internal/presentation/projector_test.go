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
