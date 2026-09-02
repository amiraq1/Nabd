package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nabd/internal/provider"
)

type recSink struct{ events []Event }

func (s *recSink) Emit(e Event) error {
	s.events = append(s.events, e)
	return nil
}

// TestRunUsesInjectedContextFields: the loop must read EstimateMessages and
// CompactBudget from the struct at run time. A pressure gate that ignores
// the injected estimator would let every compaction test pass against dead
// fields — certifying nothing.
func TestRunUsesInjectedContextFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"حسام"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
	sink := &recSink{}
	l := &Loop{
		Provider: prov,
		Budget:   NewBudget(),
		Sink:     sink,
		System:   "s",
		// Distinct signature: 9999 tokens per message. The default chars/4
		// rule could never return an exact multiple of 9999 matching the
		// message count — such a number in the journal can only have come
		// from this injected function.
		EstimateMessages: func(ms []provider.Message) int { return 9999 * len(ms) },
		CompactBudget:    12345,
		KeepFullRounds:   1,
	}
	readOut := strings.Repeat("سطر طويل\n", 400)
	for i := 0; i < 4; i++ {
		l.emit(Event{Type: UserMsg, Text: fmt.Sprintf("سؤال %d", i)})
		id := fmt.Sprintf("r%d", i)
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: id, Name: "read_file", Args: json.RawMessage(`{"path":"big.go"}`)}})
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: id, OK: true, Output: readOut}})
		l.emit(Event{Type: TurnEnd})
	}
	if err := l.Run(context.Background(), "تحليل"); err != nil {
		t.Fatal(err)
	}

	var (
		compact         *Event
		compactedNotice bool
		resumeSeq       int
	)
	for i := range sink.events {
		e := sink.events[i]
		if e.Type == Compact {
			compact = &sink.events[i]
		}
		if e.Type == Notice && strings.Contains(e.Text, "context compacted") {
			compactedNotice = true
		}
		if e.Type == UserMsg && e.Text == "تحليل" {
			resumeSeq = e.Seq
		}
	}
	if compact == nil {
		t.Fatal("Run never compacted — injected EstimateMessages/CompactBudget are dead code")
	}
	if compact.FirstKept != resumeSeq {
		t.Errorf("FirstKept=%d, want the newest user turn (seq %d): with the injected CompactBudget=12345 the tail from the newest turn exceeds the target only at older turns, so the boundary must sit at the resume turn", compact.FirstKept, resumeSeq)
	}
	st := compact.Compact
	if st == nil {
		t.Fatal("Compact event carries no CompactionStats")
	}
	if st.TokensBefore == 0 || st.TokensBefore%9999 != 0 || st.TokensBefore/9999 != st.MessagesBefore {
		t.Errorf("TokensBefore=%d over %d messages is not the injected estimator's signature (9999/message)", st.TokensBefore, st.MessagesBefore)
	}
	if st.TokensAfter >= st.TokensBefore {
		t.Errorf("compaction must shrink the estimate: before=%d after=%d", st.TokensBefore, st.TokensAfter)
	}
	if !compactedNotice {
		t.Error("successful compaction must emit the 'context compacted' notice")
	}
	t.Logf("COMPACTED: first_kept=%d tokens %d → %d over %d → %d messages",
		compact.FirstKept, st.TokensBefore, st.TokensAfter, st.MessagesBefore, st.MessagesAfter)
}

// TestContextFieldsDefaultsAndOverrides: the accessors fall back to the
// package constants only when the field is unset — and Run calls these
// accessors, so the fields are live wiring, not decoration.
func TestContextFieldsDefaultsAndOverrides(t *testing.T) {
	b := NewBudget()
	l := &Loop{Budget: b, KeepFullRounds: 2, CompactBudget: 555}
	if got := l.keepFullRounds(); got != 2 {
		t.Errorf("keepFullRounds()=%d, want injected 2", got)
	}
	if got := l.compactTarget(); got != 555 {
		t.Errorf("compactTarget()=%d, want injected 555", got)
	}
	zero := &Loop{Budget: b}
	if got := zero.keepFullRounds(); got != KeepFullRounds {
		t.Errorf("zero-value keepFullRounds()=%d, want package default %d", got, KeepFullRounds)
	}
	if got := zero.compactTarget(); got != b.Usable()*4/10 {
		t.Errorf("zero-value compactTarget()=%d, want %d", got, b.Usable()*4/10)
	}
	// pressure() multiplies the injected estimate by the budget ratio.
	est := &Loop{Budget: b, EstimateMessages: func(ms []provider.Message) int { return 500 }}
	want := float64(500) * b.Ratio() / float64(b.Usable())
	if got := est.pressure(nil); got != want {
		t.Errorf("pressure()=%v, want %v", got, want)
	}
}
