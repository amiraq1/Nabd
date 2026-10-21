package presentation

import (
	"testing"

	"nabd/internal/event"
)

func drainKeys(t *testing.T, p *Projector) map[ItemKey]bool {
	t.Helper()
	out := make(map[ItemKey]bool)
	for _, k := range p.DrainTouched() {
		out[k] = true
	}
	return out
}

// TestDrainTouchedCoversAllMutationPaths verifies the L15 contract: every
// event that creates or mutates a feed item must surface that item's key
// from DrainTouched, or the UI's fingerprint cache will serve stale lines.
func TestDrainTouchedCoversAllMutationPaths(t *testing.T) {
	p := NewProjector()
	if got := p.DrainTouched(); got != nil {
		t.Fatalf("fresh projector: DrainTouched=%v, want nil", got)
	}

	apply := func(e event.Event) {
		t.Helper()
		if err := p.Apply(e); err != nil {
			t.Fatal(err)
		}
	}

	// New user message: touched.
	apply(event.Event{Seq: 1, Type: event.UserMsg, Text: "hi"})
	keys := drainKeys(t, p)
	if !keys[ItemKey{Type: ItemUserMsg, ID: "user_1"}] {
		t.Fatalf("user msg not touched: %v", keys)
	}
	if got := p.DrainTouched(); got != nil {
		t.Fatalf("second drain after no applies: %v, want nil", got)
	}

	// Streaming deltas mutate the same assistant item in place.
	apply(event.Event{Seq: 2, Type: event.TextDelta, Text: "hel"})
	apply(event.Event{Seq: 3, Type: event.TextDelta, Text: "lo"})
	keys = drainKeys(t, p)
	asst := ItemKey{Type: ItemAssistant, ID: "asst_turn_2"}
	if !keys[asst] {
		t.Fatalf("assistant item not touched by deltas: %v", keys)
	}

	// Tool lifecycle: start creates, end mutates in place.
	apply(event.Event{Seq: 4, Type: event.ToolStart, Call: &event.ToolCall{ID: "c1", Name: "bash"}})
	tool := ItemKey{Type: ItemTool, ID: "tool_c1"}
	if keys := drainKeys(t, p); !keys[tool] {
		t.Fatalf("tool start not touched: %v", keys)
	}
	apply(event.Event{Seq: 5, Type: event.ToolEnd, Call: &event.ToolCall{ID: "c1", Name: "bash", Output: "out", OK: true}})
	if keys := drainKeys(t, p); !keys[tool] {
		t.Fatalf("tool end (in-place card update) not touched: %v", keys)
	}

	// Permission ask/reply: reply mutates the ask's card in place.
	apply(event.Event{Seq: 6, Type: event.PermAsk, Call: &event.ToolCall{ID: "c2", Name: "write_file"}})
	perm := ItemKey{Type: ItemPermission, ID: "tool_c2"}
	if keys := drainKeys(t, p); !keys[perm] {
		t.Fatalf("perm ask not touched: %v", keys)
	}
	apply(event.Event{Seq: 7, Type: event.PermReply, Decision: event.AllowOnce,
		Call: &event.ToolCall{ID: "c2", Name: "write_file"}})
	if keys := drainKeys(t, p); !keys[perm] {
		t.Fatalf("perm reply (in-place card update) not touched: %v", keys)
	}

	// Interrupted mass-cancels running tools in place.
	apply(event.Event{Seq: 8, Type: event.ToolStart, Call: &event.ToolCall{ID: "c3", Name: "bash"}})
	running := ItemKey{Type: ItemTool, ID: "tool_c3"}
	if keys := drainKeys(t, p); !keys[running] {
		t.Fatalf("second tool start not touched: %v", keys)
	}
	apply(event.Event{Seq: 9, Type: event.Interrupted, Text: "stop"})
	keys = drainKeys(t, p)
	if !keys[running] {
		t.Fatalf("interrupted did not touch running tool: %v", keys)
	}

	// Read records attach to the latest read_file card in place.
	apply(event.Event{Seq: 10, Type: event.ToolStart,
		Call: &event.ToolCall{ID: "c4", Name: "read_file"}})
	_ = drainKeys(t, p)
	apply(event.Event{Seq: 11, Type: event.EventRead,
		Read: &event.ReadRecord{Path: "f.txt", LinesRead: 3}})
	// Note: ToolStart above has no Args, so applyReadToLatest matches on
	// empty Args (rec.Path != "" && card.Args != "" fails open).
	if keys := drainKeys(t, p); !keys[ItemKey{Type: ItemTool, ID: "tool_c4"}] {
		t.Fatalf("read record (in-place card update) not touched: %v", keys)
	}
}

// TestItemKeyNoAlloc guards the hot-path requirement: keying must not
// allocate, or every frame pays it per item (see the allocation budget
// test in internal/ui).
func TestItemKeyNoAlloc(t *testing.T) {
	it := FeedItem{Type: ItemTool, ID: "tool_c1"}
	allocs := testing.AllocsPerRun(100, func() {
		_ = it.Key()
	})
	if allocs != 0 {
		t.Fatalf("ItemKey allocated %.1f allocs/run, want 0", allocs)
	}
}
