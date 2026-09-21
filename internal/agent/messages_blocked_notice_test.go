package agent

import (
	"fmt"
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
	blocked := []NoticeCategory{
		NoticeCategoryUnknown,
		NoticeCategoryCalibration,
		NoticeCategoryContextPressure,
		NoticeCategoryRateLimit,
		NoticeCategoryLengthLimit,
		NoticeCategoryTPM,
		NoticeCategoryDisplay,
	}

	const marker = "BLOCKED-NOTICE-MARKER"

	pending := func(cat NoticeCategory) []Event {
		return []Event{
			{Seq: 1, Type: UserMsg, Text: "go"},
			{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "read_file"}},
			{Seq: 3, Parent: 2, Type: Notice, Text: marker, NoticeCategory: cat},
			{Seq: 4, Parent: 3, Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "read_file", Output: "out", OK: true}},
			{Seq: 5, Parent: 4, Type: TurnEnd},
		}
	}
	idle := func(cat NoticeCategory) []Event {
		return []Event{
			{Seq: 1, Type: UserMsg, Text: "go"},
			{Seq: 2, Parent: 1, Type: Notice, Text: marker, NoticeCategory: cat},
			{Seq: 3, Parent: 2, Type: TurnEnd},
		}
	}

	for _, scenario := range []struct {
		name string
		evs  func(NoticeCategory) []Event
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
	allowed := []NoticeCategory{
		NoticeCategoryUndoResult,
		NoticeCategoryPermissionDenied,
		NoticeCategoryLoopLimit,
	}

	const marker = "ALLOWED-NOTICE-MARKER"

	for _, cat := range allowed {
		t.Run(fmt.Sprintf("category_%d", cat), func(t *testing.T) {
			evs := []Event{
				{Seq: 1, Type: UserMsg, Text: "go"},
				{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "read_file"}},
				{Seq: 3, Parent: 2, Type: Notice, Text: marker, NoticeCategory: cat},
				{Seq: 4, Parent: 3, Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "read_file", Output: "out", OK: true}},
				{Seq: 5, Parent: 4, Type: TurnEnd},
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
