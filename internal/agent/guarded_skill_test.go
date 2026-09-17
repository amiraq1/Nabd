package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"nabd/internal/provider"
	"nabd/internal/skill"
)

// guardedBody is the sentinel a skill body would leak if it ever reached
// ToolEnd.Output. Every assertion below checks it is absent from the wire.
const guardedBody = "SKILL-BODY-SENTINEL-do-not-leak"

// allowGate is a policy that permits without asking, so the tests exercise the
// execution branch and nothing else.
type allowGate struct{}

func (allowGate) Check(string) (Verdict, string)          { return VerdictAllow, "" }
func (allowGate) Record(string, Decision)                 {}
func (allowGate) Effective(_ string, d Decision) Decision { return d }

// plainFakeTools registers a tool name and runs it through the plain path. It
// deliberately does NOT implement guardedTools: this is the shape of a tool
// layer that advertises "skill" but supplies no guarded outcome.
type plainFakeTools struct {
	name    string
	runHits *int
}

func (f plainFakeTools) Specs() []provider.ToolSpec {
	return []provider.ToolSpec{{Name: f.name}}
}

func (f plainFakeTools) Run(context.Context, provider.ToolCall) (string, bool, error) {
	if f.runHits != nil {
		*f.runHits++
	}
	return guardedBody, true, nil
}

// guardedFakeTools adds the guard seam. found=false models a layer that owns
// the seam but answers "no guard for this name".
type guardedFakeTools struct {
	plainFakeTools
	guard GuardedOutcome
	found bool
}

func (f guardedFakeTools) GuardedFor(name string) (GuardedOutcome, bool) {
	if name != f.name {
		return nil, false
	}
	return f.guard, f.found
}

// fakeGuard is a stand-in for skillTool's guarded producer.
type fakeGuard struct {
	hits  *int
	err   error
	class SkillContentClass
}

func (g fakeGuard) GuardedResult(context.Context, json.RawMessage) (GuardedResult, error) {
	if g.hits != nil {
		*g.hits++
	}
	if g.err != nil {
		return GuardedResult{}, g.err
	}
	return GuardedResult{Event: Event{Type: EventSkillBody, SkillBody: &SkillBodyEvent{
		Body:  guardedBody,
		Scope: skill.ScopeProject,
		Class: g.class,
	}}, OK: true}, nil
}

func runSkillOnce(t *testing.T, tools Tools) (*recSink, error) {
	t.Helper()
	sink := &recSink{}
	l := &Loop{Tools: tools, Sink: sink, Gate: allowGate{}}
	_, err := l.runCalls(context.Background(), []provider.ToolCall{
		{ID: "c1", Name: "skill", Input: json.RawMessage(`{"name":"greet"}`)},
	})
	return sink, err
}

func toolEndOf(t *testing.T, evs []Event) Event {
	t.Helper()
	for _, e := range evs {
		if e.Type == ToolEnd {
			return e
		}
	}
	t.Fatalf("no ToolEnd among %d events", len(evs))
	return Event{}
}

func skillBodyEvents(evs []Event) []Event {
	var out []Event
	for _, e := range evs {
		if e.Type == EventSkillBody {
			out = append(out, e)
		}
	}
	return out
}

func TestLoopInputForGuardedCallIsEmpty(t *testing.T) {
	if got := loopInput(Outcome{Text: guardedBody}, true); got != "" {
		t.Fatalf("guarded loop input=%q, want empty", got)
	}
	if got := loopInput(Outcome{Text: guardedBody}, false); got != guardedBody {
		t.Fatalf("plain loop input=%q, want producer text", got)
	}
}

// Guarded calls contribute no producer text to loop detection; the body stays
// in the structured event and out of ordinary tool output.
func TestGuardedOutcomeTextNeverLeavesTheGuardedPath(t *testing.T) {
	var runHits, guardHits int
	tools := guardedFakeTools{
		plainFakeTools: plainFakeTools{name: "skill", runHits: &runHits},
		guard:          fakeGuard{hits: &guardHits, class: SkillContentClassUntrusted},
		found:          true,
	}
	sink := &recSink{}
	l := &Loop{Tools: tools, Sink: sink, Gate: allowGate{}}
	calls := make([]provider.ToolCall, 5)
	for i := range calls {
		calls[i] = provider.ToolCall{ID: fmt.Sprintf("c%d", i+1), Name: "skill", Input: json.RawMessage(`{"name":"greet"}`)}
	}
	interrupted, err := l.runCalls(context.Background(), calls)
	if interrupted || !errors.Is(err, ErrToolLoop) {
		t.Fatalf("repeated guarded calls: interrupted=%v err=%v", interrupted, err)
	}
	if guardHits != 5 || runHits != 0 {
		t.Fatalf("guardHits=%d runHits=%d, want 5 and 0", guardHits, runHits)
	}
	notices := 0
	for _, e := range sink.events {
		if e.Type == ToolEnd && e.Call.Output != "" {
			t.Fatalf("guarded output leaked: %q", e.Call.Output)
		}
		if e.Type == Notice && strings.Contains(e.Text, guardedBody) {
			t.Fatalf("guarded body leaked into notice: %q", e.Text)
		}
		if e.Type == Notice && e.NoticeCategory == NoticeCategoryLoopLimit {
			notices++
		}
		if e.Type == RunError && strings.Contains(e.Err, guardedBody) {
			t.Fatalf("guarded body leaked into abort: %q", e.Err)
		}
	}
	if notices != 2 {
		t.Fatalf("loop notices=%d, want notice at 3 and abort notice at 5", notices)
	}
}

func TestGuardedOutcomeProjectsBodyAndSkipsPlainExecution(t *testing.T) {
	var runHits, guardHits int
	tools := guardedFakeTools{
		plainFakeTools: plainFakeTools{name: "skill", runHits: &runHits},
		guard:          fakeGuard{hits: &guardHits, class: SkillContentClassUntrusted},
		found:          true,
	}

	sink, err := runSkillOnce(t, tools)
	if err != nil {
		t.Fatalf("a satisfied guard must not fail the turn: %v", err)
	}
	if runHits != 0 {
		t.Fatalf("plain execution ran %d times under a satisfied guard; the guarded path must be the only path", runHits)
	}
	if guardHits != 1 {
		t.Fatalf("guard hits = %d, want exactly 1", guardHits)
	}

	bodies := skillBodyEvents(sink.events)
	if len(bodies) != 1 {
		t.Fatalf("EventSkillBody count = %d, want 1", len(bodies))
	}
	if got := bodies[0].SkillBody.Body; got != guardedBody {
		t.Fatalf("skill body event body = %q", got)
	}
	if got := bodies[0].SkillBody.Class; got != SkillContentClassUntrusted {
		t.Fatalf("skill body class = %v, want the fixed untrusted class", got)
	}

	end := toolEndOf(t, sink.events)
	if !end.Call.OK {
		t.Fatal("a guarded load that produced an event must report OK")
	}
	if strings.Contains(end.Call.Output, guardedBody) {
		t.Fatalf("skill body leaked into ToolEnd.Output: %q", end.Call.Output)
	}
	if end.Call.Output != "" {
		t.Fatalf("guarded ToolEnd.Output must be empty, got %q", end.Call.Output)
	}
}

// The body-bearing event must be emitted before the ToolEnd that answers the
// call, so a replay that stops at ToolEnd never sees a body it cannot attribute.
func TestGuardedBodyEventPrecedesToolEnd(t *testing.T) {
	var runHits, guardHits int
	tools := guardedFakeTools{
		plainFakeTools: plainFakeTools{name: "skill", runHits: &runHits},
		guard:          fakeGuard{hits: &guardHits, class: SkillContentClassUntrusted},
		found:          true,
	}
	sink, err := runSkillOnce(t, tools)
	if err != nil {
		t.Fatal(err)
	}
	bodyIdx, endIdx := -1, -1
	for i, e := range sink.events {
		switch e.Type {
		case EventSkillBody:
			bodyIdx = i
		case ToolEnd:
			endIdx = i
		}
	}
	if bodyIdx < 0 || endIdx < 0 || bodyIdx > endIdx {
		t.Fatalf("skill body event (idx %d) must precede ToolEnd (idx %d)", bodyIdx, endIdx)
	}
}

// A guarded name with no guard must fail closed: no plain execution, no body on
// the wire, and the turn returns an error. Both shapes of the hole are covered:
// a layer without the guard seam at all, and a layer whose GuardedFor answers
// false for a name it registers.
func TestGuardedNameWithoutGuardFailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		tools func(*int) Tools
	}{
		{
			name: "layer does not implement the guard seam",
			tools: func(hits *int) Tools {
				return plainFakeTools{name: "skill", runHits: hits}
			},
		},
		{
			name: "guard seam present but answers false",
			tools: func(hits *int) Tools {
				return guardedFakeTools{plainFakeTools: plainFakeTools{name: "skill", runHits: hits}, found: false}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var runHits int
			sink, err := runSkillOnce(t, tc.tools(&runHits))
			if err == nil {
				t.Fatal("a guarded name with no guard must fail the turn")
			}
			if runHits != 0 {
				t.Fatalf("plain execution ran %d times; a guarded name must never fall back to l.exec", runHits)
			}
			if len(skillBodyEvents(sink.events)) != 0 {
				t.Fatal("a refused guarded call must not emit a body event")
			}
			end := toolEndOf(t, sink.events)
			if end.Call.OK {
				t.Fatal("the refused call must report OK=false")
			}
			if strings.Contains(end.Call.Output, guardedBody) {
				t.Fatalf("body leaked into the refusal: %q", end.Call.Output)
			}
			if !strings.Contains(end.Call.Output, "guarded") {
				t.Fatalf("the refusal must name the guarded contract, got %q", end.Call.Output)
			}
		})
	}
}

// A product error from the guard (unknown skill, changed body) must surface as
// the tool result, not be masked and not be turned into a fatal run error.
func TestGuardedProductErrorIsNotMasked(t *testing.T) {
	var runHits, guardHits int
	productErr := errors.New(`unknown skill "greet"; the skill index lists the available names`)
	tools := guardedFakeTools{
		plainFakeTools: plainFakeTools{name: "skill", runHits: &runHits},
		guard:          fakeGuard{hits: &guardHits, err: productErr},
		found:          true,
	}

	sink, err := runSkillOnce(t, tools)
	if err != nil {
		t.Fatalf("a product-level guard error is a tool result, not a fatal run error: %v", err)
	}
	if runHits != 0 {
		t.Fatal("plain execution must not run when the guard answered")
	}
	if len(skillBodyEvents(sink.events)) != 0 {
		t.Fatal("a failed load must not emit a body event")
	}
	end := toolEndOf(t, sink.events)
	if end.Call.OK {
		t.Fatal("a failed guarded load must report OK=false")
	}
	if end.Call.Output != productErr.Error() {
		t.Fatalf("product error was masked: ToolEnd.Output = %q, want %q", end.Call.Output, productErr.Error())
	}
}
