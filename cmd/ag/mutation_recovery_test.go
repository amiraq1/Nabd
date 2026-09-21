package main

import (
	"testing"

	"nabd/internal/agent"
)

func TestUnresolvedMutationIntentRequiresExplicitRecovery(t *testing.T) {
	rec := &agent.EditRecord{MutationID: "m1", Path: "notes.txt"}
	evs := []agent.Event{
		{Type: agent.EventEditIntent, Edit: rec},
	}
	if got := unresolvedMutationIntents(evs); got != 1 {
		t.Fatalf("unresolved intents=%d, want 1", got)
	}
}

func TestCommittedOrAbortedMutationIsNotUnresolved(t *testing.T) {
	tests := []struct {
		name string
		tail agent.Event
	}{
		{name: "committed", tail: agent.Event{Type: agent.EventEdit}},
		{name: "aborted", tail: agent.Event{Type: agent.EventEditAbort}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &agent.EditRecord{MutationID: "m1", Path: "notes.txt"}
			tail := tc.tail
			tail.Edit = rec
			evs := []agent.Event{
				{Type: agent.EventEditIntent, Edit: rec},
				tail,
			}
			if got := unresolvedMutationIntents(evs); got != 0 {
				t.Fatalf("unresolved intents=%d, want 0", got)
			}
		})
	}
}

func TestUnresolvedMutationIgnoresAbandonedBranch(t *testing.T) {
	rec := &agent.EditRecord{MutationID: "abandoned", Path: "old.txt"}
	active := &agent.EditRecord{MutationID: "active", Path: "new.txt"}
	evs := []agent.Event{
		{Seq: 1, Type: agent.EventEditIntent, Edit: rec},
		{Seq: 2, Parent: 0, Type: agent.Rewind, FirstKept: 0},
		{Seq: 3, Parent: 2, Type: agent.EventEditIntent, Edit: active},
	}
	if got := unresolvedMutationIntents(evs); got != 1 {
		t.Fatalf("unresolved intents=%d, want 1 for active branch only", got)
	}
}
