package agent

import (
	"fmt"
	"nabd/internal/event"
	"strings"
	"testing"

	"nabd/internal/provider"
)

// TestBlockedNoticeCategoriesDroppedWhileToolCallsPending is the regression
// guard for the TECH_DEBT entry NOTICE_QUEUE_BLOCKED_CATEGORY_UNTESTED.
// TestPendingNoticesBoundedCap exercises maxPendingNotices only with
// NoticeCategoryUndoResult (an allowed category). A regression in
// messages.go could queue blocked or unclassified categories into
// pendingNotices while tool calls are open and flush them to the model.
// Every category outside the noticeReachesModel allowlist must be dropped,
// whether or not tool calls are pending.
func TestBlockedNoticeCategoriesDroppedWhileToolCallsPending(t *testing.T) {
	blocked := []event.NoticeCategory{
		event.NoticeCategoryUnknown,
		event.NoticeCategoryCalibration,
		event.NoticeCategoryContextPressure,
		event.NoticeCategoryRateLimit,
		event.NoticeCategoryLengthLimit,
		event.NoticeCategoryTPM,
		event.NoticeCategoryDisplay,
	}

	const marker = "BLOCKED-NOTICE-MARKER"

	pending := func(cat event.NoticeCategory) []event.Event {
		return []event.Event{
			{Seq: 1, Type: event.UserMsg, Text: "go"},
			{Seq: 2, Parent: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "t1", Name: "read_file"}},
			{Seq: 3, Parent: 2, Type: event.Notice, Text: marker, NoticeCategory: cat},
			{Seq: 4, Parent: 3, Type: event.ToolEnd, Call: &event.ToolCall{ID: "t1", Name: "read_file", Output: "out", OK: true}},
			{Seq: 5, Parent: 4, Type: event.TurnEnd},
		}
	}
	idle := func(cat event.NoticeCategory) []event.Event {
		return []event.Event{
			{Seq: 1, Type: event.UserMsg, Text: "go"},
			{Seq: 2, Parent: 1, Type: event.Notice, Text: marker, NoticeCategory: cat},
			{Seq: 3, Parent: 2, Type: event.TurnEnd},
		}
	}

	for _, scenario := range []struct {
		name string
		evs  func(event.NoticeCategory) []event.Event
	}{
		{"tool_calls_pending", pending},
		{"idle", idle},
	} {
		for _, cat := range blocked {
			t.Run(fmt.Sprintf("%s/category_%d", scenario.name, cat), func(t *testing.T) {
				msgs := Messages(scenario.evs(cat))
				for _, m := range msgs {
					if strings.Contains(m.Text, marker) {
						t.Fatalf("blocked notice category %d reached the model: %#v", cat, msgs)
					}
				}
			})
		}
	}
}

// TestAllowedNoticeCategoriesFlushedAfterToolCallsPending is the contrast
// case for the guard above: allowed categories MUST survive the pending
// queue and reach the model once the tool round flushes. If both tests pass,
// the drop behaviour is category-driven, not a blanket discard.
func TestAllowedNoticeCategoriesFlushedAfterToolCallsPending(t *testing.T) {
	allowed := []event.NoticeCategory{
		event.NoticeCategoryUndoResult,
		event.NoticeCategoryLoopLimit,
	}

	const marker = "ALLOWED-NOTICE-MARKER"

	for _, cat := range allowed {
		t.Run(fmt.Sprintf("category_%d", cat), func(t *testing.T) {
			evs := []event.Event{
				{Seq: 1, Type: event.UserMsg, Text: "go"},
				{Seq: 2, Parent: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "t1", Name: "read_file"}},
				{Seq: 3, Parent: 2, Type: event.Notice, Text: marker, NoticeCategory: cat},
				{Seq: 4, Parent: 3, Type: event.ToolEnd, Call: &event.ToolCall{ID: "t1", Name: "read_file", Output: "out", OK: true}},
				{Seq: 5, Parent: 4, Type: event.TurnEnd},
			}
			msgs := Messages(evs)

			found := false
			for _, m := range msgs {
				if m.Role == provider.User && strings.Contains(m.Text, "«notice» "+marker) {
					found = true
				}
			}
			if !found {
				t.Fatalf("allowed notice category %d was not flushed to the model: %#v", cat, msgs)
			}
		})
	}
}
