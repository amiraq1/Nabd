package agent

import (
	"context"
	"nabd/internal/event"
	"strings"
	"testing"
)

type fakeHuman struct {
	answer event.Decision
}

func (f *fakeHuman) Ask(ctx context.Context, call event.ToolCall) event.Decision {
	return f.answer
}

type fakeGate struct {
	checkVerdict Verdict
	checkWhy     string
	checkReason  event.PermissionReason
	effective    event.Decision
	recorded     event.Decision
	recordCalls  int
}

func (f *fakeGate) Check(tool string) (Verdict, string) { return f.checkVerdict, f.checkWhy }
func (f *fakeGate) CheckReason(tool string) (Verdict, event.PermissionReason, string) {
	return f.checkVerdict, f.checkReason, f.checkWhy
}
func (f *fakeGate) Record(tool string, d event.Decision) {
	f.recorded = d
	f.recordCalls++
}
func (f *fakeGate) Effective(tool string, d event.Decision) event.Decision { return f.effective }

func TestDecideLogsEffectiveDecision(t *testing.T) {
	h := &fakeHuman{answer: event.AllowSession}
	g := &fakeGate{
		checkVerdict: VerdictAsk,
		effective:    event.AllowOnce, // policy downgrades it
	}
	loop := &Loop{Gate: g, Human: h}

	var lastEvent event.Event
	emit := func(e event.Event) error {
		if e.Type == event.PermReply {
			lastEvent = e
		}
		return nil
	}

	loop.decide(context.Background(), event.ToolCall{Name: "bash"}, emit)

	if lastEvent.Decision != event.AllowOnce {
		t.Errorf("expected logged Decision to be AllowOnce (effective), got %v", lastEvent.Decision)
	}
	if lastEvent.RawDecision != event.AllowSession {
		t.Errorf("expected logged RawDecision to be AllowSession, got %v", lastEvent.RawDecision)
	}
	if got, _ := loop.decide(context.Background(), event.ToolCall{Name: "bash"}, emit); got != event.AllowOnce {
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
			checkReason:  event.PermissionReasonRequired,
			checkWhy:     "permission required",
			effective:    event.AllowOnce,
		},
		Human: &fakeHuman{answer: event.AllowOnce},
	}
	var events []event.Event
	got, why := loop.decide(context.Background(), event.ToolCall{ID: "c1", Name: "bash"}, func(e event.Event) error {
		events = append(events, e)
		return nil
	})
	if got != event.AllowOnce || why != "" {
		t.Fatalf("decide = %v, %q", got, why)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want ask and reply", len(events))
	}
	for _, ev := range events {
		if ev.Reason != event.PermissionReasonRequired {
			t.Fatalf("%s reason = %q", ev.Type, ev.Reason)
		}
		if ev.Text != "permission required" {
			t.Fatalf("%s fallback text = %q", ev.Type, ev.Text)
		}
	}
}

func TestDecideRefusesWhenPermissionQuestionCannotBeJournaled(t *testing.T) {
	loop := &Loop{
		Gate:  &fakeGate{checkVerdict: VerdictAsk, effective: event.AllowOnce},
		Human: &fakeHuman{answer: event.AllowOnce},
	}
	got, why := loop.decide(context.Background(), event.ToolCall{Name: "bash"}, func(event.Event) error {
		return context.Canceled
	})
	if got != event.Deny || !strings.Contains(why, "not journaled") {
		t.Fatalf("decide = %v, %q; want Deny with journal failure", got, why)
	}
}
