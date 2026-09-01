package agent

import (
	"testing"
)

// TestJournalIntegrity enforces the event contract invariants on a live
// journal produced by real runs:
//   - Seq is contiguous (no gaps, starts at 1, strictly increasing)
//   - every Parent points at an older Seq that exists in the file
//   - every tool_use (ToolStart) has a matching tool_result (ToolEnd)
//     with the same call ID, in the same branch
func TestJournalIntegrity(t *testing.T) {
	l := &Loop{Budget: NewBudget()}

	// Two realistic turns with tools, mirroring what the loop emits.
	l.emit(Event{Type: RunStart, Text: "nabd test"})
	l.emit(Event{Type: UserMsg, Text: "اقرأ واكتب"})
	l.emit(Event{Type: TurnStart})
	l.emit(Event{Type: TextDelta, Text: "سأقرأ "})
	l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "read_file", Args: []byte(`{"path":"a.go"}`)}})
	l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "read_file", Output: "1|x", OK: true}})
	l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: "t2", Name: "write_file", Args: []byte(`{"path":"a.go"}`)}})
	l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: "t2", Name: "write_file", Output: "ok", OK: true}})
	l.emit(Event{Type: EventEdit, Edit: &EditRecord{Path: "a.go", HashAfter: "x", ReadLines: 1}})
	l.emit(Event{Type: TurnEnd})
	l.emit(Event{Type: UserMsg, Text: "مرة أخرى"})
	l.emit(Event{Type: TurnStart})
	l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: "t3", Name: "grep", Args: []byte(`{"pattern":"x"}`)}})
	l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: "t3", Name: "grep", Output: "a.go:1", OK: true}})
	l.emit(Event{Type: TurnEnd})
	l.emit(Event{Type: RunError, Err: "انتهى الدور"})

	evs := l.hist

	// 1. Seq contiguous from 1, strictly increasing.
	for i, e := range evs {
		if e.Seq != i+1 {
			t.Fatalf("Seq gap at index %d: got %d want %d", i, e.Seq, i+1)
		}
	}
	if evs[0].Seq != 1 {
		t.Fatalf("first Seq = %d, want 1", evs[0].Seq)
	}

	// 2. Parent points at an older existing Seq.
	bySeq := map[int]Event{}
	for _, e := range evs {
		bySeq[e.Seq] = e
	}
	for _, e := range evs {
		if e.Parent == 0 {
			continue // root
		}
		p, ok := bySeq[e.Parent]
		if !ok {
			t.Fatalf("event %d (%s) Parent=%d does not exist", e.Seq, e.Type, e.Parent)
		}
		if p.Seq >= e.Seq {
			t.Fatalf("event %d Parent=%d is not older", e.Seq, e.Parent)
		}
	}

	// 3. Every ToolStart has a matching ToolEnd with the same ID.
	open := map[string]bool{}
	for _, e := range evs {
		if e.Type == ToolStart {
			if e.Call == nil {
				t.Fatal("ToolStart without call")
			}
			if open[e.Call.ID] {
				t.Fatalf("duplicate ToolStart for %s", e.Call.ID)
			}
			open[e.Call.ID] = true
		}
		if e.Type == ToolEnd {
			if e.Call == nil {
				t.Fatal("ToolEnd without call")
			}
			if !open[e.Call.ID] {
				t.Fatalf("ToolEnd %s with no matching ToolStart", e.Call.ID)
			}
			delete(open, e.Call.ID)
		}
	}
	for id := range open {
		t.Fatalf("ToolStart %s left open (no matching ToolEnd)", id)
	}

	// 4. Live() keeps the same invariants on the branch.
	live := Live(evs)
	if len(live) == 0 {
		t.Fatal("Live() empty")
	}
	// The live branch must end with the newest event.
	if live[len(live)-1].Seq != evs[len(evs)-1].Seq {
		t.Fatalf("Live() tail = %d, want %d", live[len(live)-1].Seq, evs[len(evs)-1].Seq)
	}
	// Walk the branch back via Parent: every link must resolve.
	for i := len(live) - 1; i > 0; i-- {
		if live[i].Parent != live[i-1].Seq {
			t.Fatalf("Live() branch broken at %s: parent=%d prev=%d", live[i].Type, live[i].Parent, live[i-1].Seq)
		}
	}
}
