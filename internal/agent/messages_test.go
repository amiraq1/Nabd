package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessagesPairsToolCalls(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "start"},
		{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "t1", Args: json.RawMessage("{}")}},
		{Seq: 3, Parent: 2, Type: ToolStart, Call: &ToolCall{ID: "t2", Name: "t2", Args: json.RawMessage("{}")}},
		{Seq: 4, Parent: 3, Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "t1", Output: "out1", OK: true}},
		{Seq: 5, Parent: 4, Type: ToolEnd, Call: &ToolCall{ID: "t2", Name: "t2", Output: "out2", OK: true}},
		{Seq: 6, Parent: 5, Type: TurnEnd},
	}
	ms := Messages(evs)
	if len(ms) != 3 { // user, assistant(calls), user(results)
		t.Fatalf("expected 3 messages, got %d", len(ms))
	}
	if ms[1].Role != "assistant" || len(ms[1].ToolCalls) != 2 {
		t.Fatalf("expected assistant message with 2 calls, got %v", ms[1])
	}
	if ms[2].Role != "user" || len(ms[2].ToolResults) != 2 {
		t.Fatalf("expected user message with 2 results, got %v", ms[2])
	}
}

func TestMessagesAnswersDeadCall(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "اقرأ"},
		{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "read_file"}},
		{Seq: 3, Parent: 2, Type: Interrupted},
	}
	ms := Messages(evs)
	var calls, results int
	for _, m := range ms {
		calls += len(m.ToolCalls)
		results += len(m.ToolResults)
	}
	if calls != results {
		t.Fatalf("%d نداء مقابل %d نتيجة — الطلب التالي سيُرفض", calls, results)
	}
	if results != 1 {
		t.Fatalf("expected 1 result, got %d", results)
	}
}

func TestMessagesRoundsSplit(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "do it"},
		{Seq: 2, Parent: 1, Type: TextDelta, Text: "thinking..."},
		{Seq: 3, Parent: 2, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "test"}},
		{Seq: 4, Parent: 3, Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "test", Output: "done", OK: true}},
		{Seq: 5, Parent: 4, Type: TextDelta, Text: "done!"},
		{Seq: 6, Parent: 5, Type: TurnEnd},
	}
	ms := Messages(evs)
	if len(ms) != 4 { // user, assistant(text+call), user(result), assistant(text)
		t.Fatalf("expected 4 messages, got %d", len(ms))
	}
	if ms[1].Role != "assistant" || ms[1].Text != "thinking..." || len(ms[1].ToolCalls) != 1 {
		t.Fatalf("bad round 1: %v", ms[1])
	}
	if ms[2].Role != "user" || len(ms[2].ToolResults) != 1 {
		t.Fatalf("bad round 2: %v", ms[2])
	}
	if ms[3].Role != "assistant" || ms[3].Text != "done!" {
		t.Fatalf("bad round 3: %v", ms[3])
	}
}

func TestRewindDropsBranch(t *testing.T) {
	l := &Loop{}
	l.emit(Event{Type: UserMsg, Text: "أول"})
	l.emit(Event{Type: TextDelta, Text: "جواب أول"})
	l.emit(Event{Type: UserMsg, Text: "ثانٍ"})
	l.emit(Event{Type: TextDelta, Text: "جواب ثانٍ"})

	back, err := l.Rewind(1)
	if err != nil {
		t.Fatal(err)
	}
	if back != "ثانٍ" {
		t.Fatalf("رجع %q", back)
	}
	live := Live(l.hist)
	for _, e := range live {
		if e.Text == "جواب ثانٍ" {
			t.Fatal("الفرع المهجور ما زال حيًّا")
		}
	}
	if len(l.hist) <= len(live) {
		t.Fatal("القصّ حذف من الملف بدل أن يضيف إليه")
	}
}

func TestNoticeInjectedDuringToolCallDoesNotCancelOrPrecedeToolResult(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "start"},
		{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "read_file"}},
		{Seq: 3, Parent: 2, Type: Notice, Text: "calibrated"},
		{Seq: 4, Parent: 3, Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "read_file", Output: "file_data", OK: true}},
		{Seq: 5, Parent: 4, Type: TurnEnd},
	}
	ms := Messages(evs)
	for _, m := range ms {
		for _, tr := range m.ToolResults {
			if tr.ID == "t1" && tr.Output != "file_data" {
				t.Fatalf("tool call t1 has bad result (cancelled or corrupted): %q", tr.Output)
			}
		}
	}
	if len(ms) != 4 {
		t.Fatalf("expected 4 messages (user, assistant, user-results, user-notice), got %d: %v", len(ms), ms)
	}
	if len(ms[2].ToolResults) != 1 || ms[2].ToolResults[0].Output != "file_data" {
		t.Fatalf("expected ms[2] to be tool result, got: %v", ms[2])
	}
	if ms[3].Text != "«notice» calibrated" {
		t.Fatalf("expected ms[3] to be notice, got: %v", ms[3])
	}
}

func TestNoticePreservedAfterMultipleResults(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "start"},
		{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "cmd1"}},
		{Seq: 3, Parent: 2, Type: Notice, Text: "notice_one"},
		{Seq: 4, Parent: 3, Type: Notice, Text: "notice_two"},
		{Seq: 5, Parent: 4, Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "cmd1", Output: "res1", OK: true}},
		{Seq: 6, Parent: 5, Type: TurnEnd},
	}
	ms := Messages(evs)
	// ms[0]: user "start"
	// ms[1]: assistant tool_calls: [cmd1]
	// ms[2]: user tool_results: [res1]
	// ms[3]: user notice_one
	// ms[4]: user notice_two
	if len(ms) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(ms))
	}
	if len(ms[2].ToolResults) != 1 || ms[2].ToolResults[0].Output != "res1" {
		t.Fatalf("tool result missing or corrupted: %v", ms[2])
	}
	if ms[3].Text != "«notice» notice_one" || ms[4].Text != "«notice» notice_two" {
		t.Fatalf("notices not preserved in order: ms[3]=%q ms[4]=%q", ms[3].Text, ms[4].Text)
	}
}

func TestNoticeWithParallelToolCalls(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "start parallel"},
		{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "read_file"}},
		{Seq: 3, Parent: 2, Type: ToolStart, Call: &ToolCall{ID: "t2", Name: "read_file"}},
		{Seq: 4, Parent: 3, Type: Notice, Text: "parallel_notice"},
		{Seq: 5, Parent: 4, Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "read_file", Output: "out1", OK: true}},
		{Seq: 6, Parent: 5, Type: ToolEnd, Call: &ToolCall{ID: "t2", Name: "read_file", Output: "out2", OK: true}},
		{Seq: 7, Parent: 6, Type: TurnEnd},
	}
	ms := Messages(evs)
	// ms[0]: user "start parallel"
	// ms[1]: assistant with 2 tool calls
	// ms[2]: user with 2 tool results
	// ms[3]: user with notice
	if len(ms) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(ms), ms)
	}
	if ms[1].Role != "assistant" || len(ms[1].ToolCalls) != 2 {
		t.Fatalf("expected assistant with 2 calls, got: %v", ms[1])
	}
	if ms[2].Role != "user" || len(ms[2].ToolResults) != 2 {
		t.Fatalf("expected user with 2 tool results, got: %v", ms[2])
	}
	if ms[3].Role != "user" || ms[3].Text != "«notice» parallel_notice" {
		t.Fatalf("expected notice after both results, got: %v", ms[3])
	}
}

func TestPendingNoticesBoundedCap(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "start"},
		{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "t1", Name: "cmd"}},
	}
	for i := 0; i < 50; i++ {
		evs = append(evs, Event{
			Seq: i + 3, Parent: i + 2, Type: Notice, Text: "spam",
		})
	}
	evs = append(evs, Event{
		Seq: 53, Parent: 52, Type: ToolEnd, Call: &ToolCall{ID: "t1", Name: "cmd", Output: "ok", OK: true},
	})
	evs = append(evs, Event{Seq: 54, Parent: 53, Type: TurnEnd})

	ms := Messages(evs)
	// user, assistant, tool_results, followed by at most maxPendingNotices notices
	noticeCount := 0
	for _, m := range ms {
		if strings.HasPrefix(m.Text, "«notice» ") {
			noticeCount++
		}
	}
	if noticeCount != maxPendingNotices {
		t.Fatalf("expected notice count capped at %d, got %d", maxPendingNotices, noticeCount)
	}
}
