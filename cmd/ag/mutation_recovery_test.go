package main

import (
	"testing"

	"nabd/internal/event"
)

func TestUnresolvedMutationIntentRequiresExplicitRecovery(t *testing.T) {
	rec := &event.EditRecord{MutationID: "m1", Path: "notes.txt"}
	evs := []event.Event{
		{Type: event.EventEditIntent, Edit: rec},
	}
	if got := unresolvedMutationIntents(evs); got != 1 {
		t.Fatalf("unresolved intents=%d, want 1", got)
	}
}

func TestCommittedOrAbortedMutationIsNotUnresolved(t *testing.T) {
	tests := []struct {
		name string
		tail event.Event
	}{
		{name: "committed", tail: event.Event{Type: event.EventEdit}},
		{name: "aborted", tail: event.Event{Type: event.EventEditAbort}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &event.EditRecord{MutationID: "m1", Path: "notes.txt"}
			tail := tc.tail
			tail.Edit = rec
			evs := []event.Event{
				{Type: event.EventEditIntent, Edit: rec},
				tail,
			}
			if got := unresolvedMutationIntents(evs); got != 0 {
				t.Fatalf("unresolved intents=%d, want 0", got)
			}
		})
	}
}

func TestUnresolvedMutationIgnoresAbandonedBranch(t *testing.T) {
	rec := &event.EditRecord{MutationID: "abandoned", Path: "old.txt"}
	active := &event.EditRecord{MutationID: "active", Path: "new.txt"}
	evs := []event.Event{
		{Seq: 1, Type: event.EventEditIntent, Edit: rec},
		{Seq: 2, Parent: 0, Type: event.Rewind, FirstKept: 0},
		{Seq: 3, Parent: 2, Type: event.EventEditIntent, Edit: active},
	}
	if got := unresolvedMutationIntents(evs); got != 1 {
		t.Fatalf("unresolved intents=%d, want 1 for active branch only", got)
	}
}
