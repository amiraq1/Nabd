package agent

import (
	"bytes"
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
	var lastNotice string
	for _, m := range ms {
		if strings.HasPrefix(m.Text, "«notice» ") {
			noticeCount++
			lastNotice = m.Text
		}
	}
	if noticeCount != maxPendingNotices {
		t.Fatalf("expected notice count capped at %d, got %d", maxPendingNotices, noticeCount)
	}
	if lastNotice != "«notice» (notices truncated: cap reached)" {
		t.Fatalf("expected last notice to record truncation event, got %q", lastNotice)
	}
}

func TestReplayNoticeDuringToolCallSerializesToValidOpenAIOrder(t *testing.T) {
	// Reconstruct the exact sequence from live session where a notice was emitted
	// during a tool turn.
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "run inspection"},
		{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "call_1", Name: "read_file"}},
		{Seq: 3, Parent: 2, Type: ToolStart, Call: &ToolCall{ID: "call_2", Name: "read_file"}},
		{Seq: 4, Parent: 3, Type: Notice, Text: "calibration: token ratio adopted 1.45"},
		{Seq: 5, Parent: 4, Type: ToolEnd, Call: &ToolCall{ID: "call_1", Name: "read_file", Output: "notes content", OK: true}},
		{Seq: 6, Parent: 5, Type: ToolEnd, Call: &ToolCall{ID: "call_2", Name: "read_file", Output: "readme content", OK: true}},
		{Seq: 7, Parent: 6, Type: TurnEnd},
	}
	ms := Messages(evs)
	if len(ms) != 4 {
		t.Fatalf("expected 4 messages (user, assistant, tool_results, notice), got %d", len(ms))
	}
	if ms[1].Role != "assistant" || len(ms[1].ToolCalls) != 2 {
		t.Fatalf("expected assistant with 2 calls, got %v", ms[1])
	}
	if ms[2].Role != "user" || len(ms[2].ToolResults) != 2 {
		t.Fatalf("expected user with 2 tool results, got %v", ms[2])
	}
	if ms[3].Role != "user" || ms[3].Text != "«notice» calibration: token ratio adopted 1.45" {
		t.Fatalf("expected user notice after tool results, got %v", ms[3])
	}
}

// TestMessagesReplayIsDeterministic verifies the core determinism contract:
// replaying identical history any number of times yields byte-equivalent
// messages. This exercises mixed tool batches, partial denial (orphan ToolEnd
// synthesis over the `open` map), cancelled/interrupted rounds, and JSON
// serialization stability (Section B requirements 1 and 8).
func TestMessagesReplayIsDeterministic(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "inspect"},
		{Seq: 2, Parent: 1, Type: ToolStart, Call: &ToolCall{ID: "c1", Name: "read_file"}},
		{Seq: 3, Parent: 2, Type: ToolStart, Call: &ToolCall{ID: "c2", Name: "read_file"}},
		{Seq: 4, Parent: 3, Type: ToolStart, Call: &ToolCall{ID: "c3", Name: "bash"}},
		{Seq: 5, Parent: 4, Type: ToolEnd, Call: &ToolCall{ID: "c1", Name: "read_file", Output: "a", OK: true}},
		// c2 never closes -> orphan synthesis; c3 dies on interrupt.
		{Seq: 6, Parent: 5, Type: ToolEnd, Call: &ToolCall{ID: "c3", Name: "bash", Output: "", OK: false}},
		{Seq: 7, Parent: 6, Type: Interrupted},
		{Seq: 8, Parent: 7, Type: UserMsg, Text: "again"},
		{Seq: 9, Parent: 8, Type: TextDelta, Text: "thinking"},
		{Seq: 10, Parent: 9, Type: TurnEnd},
	}

	var first []byte
	for i := 0; i < 200; i++ {
		ms := Messages(evs)
		// Every ToolResult.ID must have a preceding matching ToolCall.ID.
		callIDs := map[string]bool{}
		for _, m := range ms {
			for _, c := range m.ToolCalls {
				callIDs[c.ID] = true
			}
		}
		for _, m := range ms {
			for _, r := range m.ToolResults {
				if r.ID == "" {
					continue
				}
				if !callIDs[r.ID] {
					t.Fatalf("replay %d: ToolResult %s has no matching ToolCall", i, r.ID)
				}
			}
		}
		b, err := json.Marshal(ms)
		if err != nil {
			t.Fatalf("replay %d: marshal: %v", i, err)
		}
		if first == nil {
			first = b
			continue
		}
		if !bytes.Equal(first, b) {
			t.Fatalf("replay %d: nondeterministic output\n first=%s\n  this=%s", i, first, b)
		}
	}
}
