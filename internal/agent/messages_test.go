package agent

import (
	"encoding/json"
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
