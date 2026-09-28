package presentation_test

import (
	"testing"

	"nabd/internal/event"
	"nabd/internal/presentation"
)

func TestToolEndPairsByCallID(t *testing.T) {
	p := presentation.NewProjector()
	events := []event.Event{
		{Seq: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "call-a", Name: "read_file", Args: []byte(`{"path":"README.md"}`)}},
		{Seq: 2, Type: event.ToolStart, Call: &event.ToolCall{ID: "call-b", Name: "read_file", Args: []byte(`{"path":"internal/agent/loop.go"}`)}},
		{Seq: 3, Type: event.ToolEnd, Call: &event.ToolCall{ID: "call-a", Name: "read_file", Output: "first", OK: true}},
	}
	for _, e := range events {
		if err := p.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	cards := toolCardsByCallID(p.Items())
	if cards["call-a"] == nil || cards["call-a"].Status != presentation.ToolDone {
		t.Fatalf("call-a=%+v, want done", cards["call-a"])
	}
	if cards["call-b"] == nil || cards["call-b"].Status != presentation.ToolRunning {
		t.Fatalf("call-b=%+v, want running", cards["call-b"])
	}
}

func TestReadFileCarriesNextOffset(t *testing.T) {
	p := presentation.NewProjector()
	for _, e := range []event.Event{
		{Seq: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "read-a", Name: "read_file", Args: []byte(`{"path":"README.md","offset":132}`)}},
		{Seq: 2, Type: event.ToolEnd, Call: &event.ToolCall{ID: "read-a", Name: "read_file", Output: "132|hello", OK: true}},
		{Seq: 3, Type: event.EventRead, Read: &event.ReadRecord{Path: "README.md", Truncated: true, NextOffset: 170}},
	} {
		if err := p.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	card := toolCardsByCallID(p.Items())["read-a"]
	if card == nil || !card.Truncated || card.NextOffset == nil || *card.NextOffset != 170 {
		t.Fatalf("card=%+v, want truncated next=170", card)
	}
	if card.OutputState != presentation.OutputSaved {
		t.Fatalf("state=%q, want saved", card.OutputState)
	}
}

func TestReadRecordBeforeToolEndStillAttaches(t *testing.T) {
	p := presentation.NewProjector()
	for _, e := range []event.Event{
		{Seq: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "read-a", Name: "read_file", Args: []byte(`{"path":"README.md"}`)}},
		{Seq: 2, Type: event.EventRead, Read: &event.ReadRecord{Path: "README.md", Truncated: true, NextOffset: 170}},
		{Seq: 3, Type: event.ToolEnd, Call: &event.ToolCall{ID: "read-a", Name: "read_file", Output: "content", OK: true}},
	} {
		if err := p.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	card := toolCardsByCallID(p.Items())["read-a"]
	if card == nil || card.NextOffset == nil || *card.NextOffset != 170 {
		t.Fatalf("card=%+v", card)
	}
}

func TestOutputStatesDistinguishExecutionAndPersistenceTruncation(t *testing.T) {
	p := presentation.NewProjector()
	for _, e := range []event.Event{
		{Seq: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "read-a", Name: "read_file", Args: []byte(`{"path":"README.md"}`)}},
		{Seq: 2, Type: event.ToolEnd, Call: &event.ToolCall{ID: "read-a", Name: "read_file", Output: "part\n...[truncated 100 bytes]", OK: true}},
		{Seq: 3, Type: event.EventRead, Read: &event.ReadRecord{Path: "README.md", Truncated: true, NextOffset: 20}},
	} {
		_ = p.Apply(e)
	}
	card := toolCardsByCallID(p.Items())["read-a"]
	if card == nil || card.OutputState != presentation.OutputTruncated || !card.Truncated {
		t.Fatalf("card=%+v", card)
	}
}

func TestMissingOutputIsNotShownAsSaved(t *testing.T) {
	p := presentation.NewProjector()
	_ = p.Apply(event.Event{Seq: 1, Type: event.ToolEnd, Call: &event.ToolCall{ID: "empty", Name: "bash", OK: true}})
	card := toolCardsByCallID(p.Items())["empty"]
	if card == nil || card.OutputState != presentation.OutputNone {
		t.Fatalf("card=%+v, want none", card)
	}
	orphan := presentation.NewProjector()
	_ = orphan.Apply(event.Event{Seq: 1, Type: event.EventRead, Read: &event.ReadRecord{Path: "lost.go", Truncated: true, NextOffset: 10}})
	_ = orphan.Apply(event.Event{Seq: 2, Type: event.TurnEnd})
	items := orphan.Items()
	if len(items) != 1 || items[0].Tool == nil || items[0].Tool.OutputState != presentation.OutputUnavailable {
		t.Fatalf("items=%+v", items)
	}
}

func TestPermissionDenialIsNotConfusedWithToolFailure(t *testing.T) {
	p := presentation.NewProjector()
	for _, e := range []event.Event{
		{Seq: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "denied", Name: "bash"}},
		{Seq: 2, Type: event.PermAsk, Call: &event.ToolCall{ID: "denied", Name: "bash"}},
		{Seq: 3, Type: event.PermReply, Call: &event.ToolCall{ID: "denied", Name: "bash"}, Decision: event.Deny, RawDecision: event.Deny},
		{Seq: 4, Type: event.ToolEnd, Call: &event.ToolCall{ID: "denied", Name: "bash", Output: "refused", OK: false}},
		{Seq: 5, Type: event.ToolStart, Call: &event.ToolCall{ID: "failed", Name: "read_file"}},
		{Seq: 6, Type: event.ToolEnd, Call: &event.ToolCall{ID: "failed", Name: "read_file", Output: "not found", OK: false}},
	} {
		_ = p.Apply(e)
	}
	cards := toolCardsByCallID(p.Items())
	if cards["denied"].Status != presentation.ToolDenied || cards["failed"].Status != presentation.ToolFailed {
		t.Fatalf("cards=%+v", cards)
	}
}

func TestToolCardNextOffsetIsDeepCopied(t *testing.T) {
	p := presentation.NewProjector()
	for _, e := range []event.Event{
		{Seq: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "r", Name: "read_file", Args: []byte(`{"path":"a.go"}`)}},
		{Seq: 2, Type: event.ToolEnd, Call: &event.ToolCall{ID: "r", Name: "read_file", Output: "1|x", OK: true}},
		{Seq: 3, Type: event.EventRead, Read: &event.ReadRecord{Path: "a.go", Truncated: true, NextOffset: 2}},
	} {
		_ = p.Apply(e)
	}
	items := p.Items()
	*items[0].Tool.NextOffset = 999
	fresh := p.Items()
	if *fresh[0].Tool.NextOffset != 2 {
		t.Fatalf("mutation leaked")
	}
}

func toolCardsByCallID(items []presentation.FeedItem) map[string]*presentation.ToolCard {
	out := map[string]*presentation.ToolCard{}
	for _, item := range items {
		if item.Tool != nil {
			out[item.Tool.CallID] = item.Tool
		}
	}
	return out
}
