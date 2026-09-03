// Package agent: gate.go is the only place a tool call can be stopped
// before it runs. The loop never inspects arguments and never guesses.
package agent

import "context"

// Verdict is what the policy says without troubling the human.
// The zero value asks: silence is never consent.
type Verdict int

const (
	VerdictAsk Verdict = iota
	VerdictAllow
	VerdictDeny
)

// Gate is the policy. Check is asked about a tool name, never a path:
// a human cannot audit a glob in half a second, but can audit an intent.
// Effective reports what a decision actually means once policy constraints
// are applied (e.g. AllowSession for an Executing tool becomes AllowOnce),
// so the journal records the grant that was given rather than clicked.
type Gate interface {
	Check(tool string) (Verdict, string)
	Record(tool string, d Decision)
	Effective(tool string, d Decision) Decision
}

// Asker is the human, reached through the UI. It must return on ctx death.
type Asker interface {
	Ask(ctx context.Context, call ToolCall) Decision
}

// decide emits the question and the answer into the journal, so a replay
// shows not only what ran but what was permitted, and by whom. The
// PermReply event records both the raw user decision and the effective
// decision actually applied after policy constraints (e.g. AllowSession
// for an Executing tool becomes AllowOnce).
func (l *Loop) decide(ctx context.Context, c ToolCall, emit func(Event) error) (Decision, string) {
	if l.Gate == nil {
		return Deny, "no permission gate installed"
	}
	v, why := l.Gate.Check(c.Name)
	switch v {
	case VerdictAllow:
		return AllowOnce, ""
	case VerdictDeny:
		if why == "" {
			why = "unknown or forbidden tool"
		}
		emit(Event{Type: PermReply, Call: &c, Decision: Deny, EffectiveDecision: Deny, Text: why})
		return Deny, why
	}
	if l.Human == nil {
		emit(Event{Type: PermReply, Call: &c, Decision: Deny, EffectiveDecision: Deny, Text: "no prompt interface"})
		return Deny, "no prompt interface"
	}
	emit(Event{Type: PermAsk, Call: &c, Text: why})
	d := l.Human.Ask(ctx, c)
	if ctx.Err() != nil {
		d = Deny // ctrl+c must never widen permission
	}
	// Apply policy constraints: the effective decision may differ from the
	// raw click (e.g. AllowSession for bash → AllowOnce).
	effective := l.Gate.Effective(c.Name, d)
	emit(Event{Type: PermReply, Call: &c, Decision: d, EffectiveDecision: effective})
	if effective == AllowSession {
		l.Gate.Record(c.Name, effective)
	}
	return d, ""
}
