package ui

import (
	"testing"

	"nabd/internal/event"
)

// TestReplayBuildsHistoryFromLiveEvents: building a feed from a live event
// branch populates the UI history from user_msg events only. Messages on
// abandoned (rewound) branches never enter history because the caller feeds
// event.Live(...), which follows only the current parent chain.
func TestReplayBuildsHistoryFromLiveEvents(t *testing.T) {
	// Main branch 1→7, then a rewind forks at seq 4 (Parent 4) and the
	// session continues 8→11. Seq 5-7 are abandoned.
	events := []event.Event{
		{Seq: 1, Parent: 0, Type: event.RunStart, Text: "start"},
		{Seq: 2, Parent: 1, Type: event.UserMsg, Text: "first message"},
		{Seq: 3, Parent: 2, Type: event.TextDelta, Text: "reply one"},
		{Seq: 4, Parent: 3, Type: event.TurnEnd},
		{Seq: 5, Parent: 4, Type: event.UserMsg, Text: "second message (abandoned)"},
		{Seq: 6, Parent: 5, Type: event.TextDelta, Text: "reply two"},
		{Seq: 7, Parent: 6, Type: event.TurnEnd},
		// Rewind: fork from seq 4; the branch above is abandoned.
		{Seq: 8, Parent: 4, Type: event.UserMsg, Text: "third message"},
		{Seq: 9, Parent: 8, Type: event.TextDelta, Text: "reply three"},
		{Seq: 10, Parent: 9, Type: event.TurnEnd},
		{Seq: 11, Parent: 10, Type: event.RunEnd, Text: "end"},
	}

	live := event.Live(events)
	// The live branch must contain the first and third user messages only.
	f := NewFeed()
	f.BuildFromEvents(live)

	if f.HistoryLen() != 2 {
		t.Fatalf("history from live branch = %d, want 2 (first + third; second was rewound)", f.HistoryLen())
	}
}
