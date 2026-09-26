package agent

import (
	"context"
	"strings"
	"testing"
)

type fakeHuman struct {
	answer Decision
}

func (f *fakeHuman) Ask(ctx context.Context, call ToolCall) Decision {
	return f.answer
}

type fakeGate struct {
	checkVerdict Verdict
	checkWhy     string
	checkReason  PermissionReason
	effective    Decision
	recorded     Decision
	recordCalls  int
}

func (f *fakeGate) Check(tool string) (Verdict, string) { return f.checkVerdict, f.checkWhy }
func (f *fakeGate) CheckReason(tool string) (Verdict, PermissionReason, string) {
	return f.checkVerdict, f.checkReason, f.checkWhy
}
func (f *fakeGate) Record(tool string, d Decision) {
	f.recorded = d
	f.recordCalls++
}
func (f *fakeGate) Effective(tool string, d Decision) Decision { return f.effective }

func TestDecideLogsEffectiveDecision(t *testing.T) {
	h := &fakeHuman{answer: AllowSession}
	g := &fakeGate{
		checkVerdict: VerdictAsk,
		effective:    AllowOnce, // policy downgrades it
	}
	loop := &Loop{Gate: g, Human: h}

	var lastEvent Event
	emit := func(e Event) error {
		if e.Type == PermReply {
			lastEvent = e
		}
		return nil
	}

	loop.decide(context.Background(), ToolCall{Name: "bash"}, emit)

	if lastEvent.Decision != AllowOnce {
		t.Errorf("expected logged Decision to be AllowOnce (effective), got %v", lastEvent.Decision)
	}
	if lastEvent.RawDecision != AllowSession {
		t.Errorf("expected logged RawDecision to be AllowSession, got %v", lastEvent.RawDecision)
	}
	if got, _ := loop.decide(context.Background(), ToolCall{Name: "bash"}, emit); got != AllowOnce {
		t.Errorf("decide returned %v, want effective AllowOnce", got)
	}
	if g.recordCalls != 0 {
		t.Errorf("downgraded decision was recorded %v times, want no session grant", g.recordCalls)
	}
}

func TestDecidePersistsStablePermissionReason(t *testing.T) {
	loop := &Loop{
		Gate: &fakeGate{
			checkVerdict: VerdictAsk,
			checkReason:  PermissionReasonRequired,
			checkWhy:     "permission required",
			effective:    AllowOnce,
		},
		Human: &fakeHuman{answer: AllowOnce},
	}
	var events []Event
	got, why := loop.decide(context.Background(), ToolCall{ID: "c1", Name: "bash"}, func(e Event) error {
		events = append(events, e)
		return nil
	})
	if got != AllowOnce || why != "" {
		t.Fatalf("decide = %v, %q", got, why)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want ask and reply", len(events))
	}
	for _, event := range events {
		if event.Reason != PermissionReasonRequired {
			t.Fatalf("%s reason = %q", event.Type, event.Reason)
		}
		if event.Text != "permission required" {
			t.Fatalf("%s fallback text = %q", event.Type, event.Text)
		}
	}
}

func TestDecideRefusesWhenPermissionQuestionCannotBeJournaled(t *testing.T) {
	loop := &Loop{
		Gate:  &fakeGate{checkVerdict: VerdictAsk, effective: AllowOnce},
		Human: &fakeHuman{answer: AllowOnce},
	}
	got, why := loop.decide(context.Background(), ToolCall{Name: "bash"}, func(Event) error {
		return context.Canceled
	})
	if got != Deny || !strings.Contains(why, "not journaled") {
		t.Fatalf("decide = %v, %q; want Deny with journal failure", got, why)
	}
}
