package agent_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/event"
	"nabd/internal/store"
)

// This exercises the current journal writer and reader, not every historical
// reader. Unknown numeric categories must survive storage but not reach a model.
func TestJournalNoticeCategoryRoundTripDoesNotReachModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		cat  event.NoticeCategory
	}{
		{"provider_empty", event.NoticeCategoryProviderEmpty},
		{"unknown_99", event.NoticeCategory(99)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "notice.jsonl")
			journal, err := store.NewJSONL(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = journal.Close() })
			events := []event.Event{
				{Seq: 1, Type: event.UserMsg, Text: "hello"},
				{Seq: 2, Parent: 1, Type: event.Notice, NoticeCategory: tc.cat, Text: "BLOCKED-JOURNAL-NOTICE"},
				{Seq: 3, Parent: 2, Type: event.TextDelta, Text: "answer"},
				{Seq: 4, Parent: 3, Type: event.TurnEnd},
			}
			for _, ev := range events {
				if err := journal.Append(ev); err != nil {
					t.Fatal(err)
				}
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Read(path)
			if err != nil {
				t.Fatalf("current journal reader rejected category %d: %v", tc.cat, err)
			}
			if len(loaded) != len(events) {
				t.Fatalf("loaded %d events, want %d", len(loaded), len(events))
			}
			if loaded[1].Type != event.Notice || loaded[1].NoticeCategory != tc.cat ||
				loaded[1].Text != events[1].Text {
				t.Fatalf("notice did not survive journal round trip: %+v", loaded[1])
			}
			withoutNotice := []event.Event{loaded[0], loaded[2], loaded[3]}
			want := agent.Messages(withoutNotice)
			if len(want) == 0 {
				t.Fatal("control projection must contain the user and assistant messages")
			}
			if got := agent.Messages(loaded); !reflect.DeepEqual(got, want) {
				t.Fatalf("blocked persisted notice changed model messages:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}
