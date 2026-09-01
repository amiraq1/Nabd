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
type Gate interface {
	Check(tool string) (Verdict, string)
	Record(tool string, d Decision)
}

// Asker is the human, reached through the UI. It must return on ctx death.
type Asker interface {
	Ask(ctx context.Context, call ToolCall) Decision
}

// decide emits the question and the answer into the journal, so a replay
// shows not only what ran but what was permitted, and by whom.
func (l *Loop) decide(ctx context.Context, c ToolCall, emit func(Event) error) (Decision, string) {
	if l.Gate == nil {
		return Deny, "لا بوابة أذونات مركّبة"
	}
	v, why := l.Gate.Check(c.Name)
	switch v {
	case VerdictAllow:
		return AllowOnce, ""
	case VerdictDeny:
		if why == "" {
			why = "أداة غير معروفة أو ممنوعة"
		}
		emit(Event{Type: PermReply, Call: &c, Decision: Deny, Text: why})
		return Deny, why
	}
	if l.Human == nil {
		emit(Event{Type: PermReply, Call: &c, Decision: Deny, Text: "لا واجهة للسؤال"})
		return Deny, "لا واجهة للسؤال"
	}
	emit(Event{Type: PermAsk, Call: &c, Text: why})
	d := l.Human.Ask(ctx, c)
	if ctx.Err() != nil {
		d = Deny // ctrl+c must never widen permission
	}
	emit(Event{Type: PermReply, Call: &c, Decision: d})
	if d == AllowSession {
		l.Gate.Record(c.Name, d)
	}
	return d, ""
}
