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

// ReasonedGate is the additive permission-reason contract. Keeping it optional
// preserves source compatibility for embedders and test gates while the built-in
// policy emits stable codes for every new permission event.
type ReasonedGate interface {
	CheckReason(tool string) (Verdict, PermissionReason, string)
}

// SessionGrantPolicy is optional so existing test gates and integrations keep
// compiling. The UI uses it only when the policy can explicitly describe
// whether an AllowSession choice is valid for this tool.
type SessionGrantPolicy interface {
	SessionGrantAllowed(tool string) bool
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
	v, reason, why := checkPermission(l.Gate, c.Name)
	switch v {
	case VerdictAllow:
		return AllowOnce, ""
	case VerdictDeny:
		if why == "" {
			why = "unknown or forbidden tool"
			reason = PermissionReasonUnknownOrForbidden
		}
		if err := emit(Event{Type: PermReply, Call: &c, Decision: Deny, RawDecision: Deny, Text: why, Reason: reason}); err != nil {
			return Deny, "permission decision was not journaled: " + err.Error()
		}
		return Deny, why
	}
	if l.Human == nil {
		const noPrompt = "no prompt interface"
		if err := emit(Event{Type: PermReply, Call: &c, Decision: Deny, RawDecision: Deny, Text: noPrompt, Reason: PermissionReasonNoPrompt}); err != nil {
			return Deny, "permission decision was not journaled: " + err.Error()
		}
		return Deny, noPrompt
	}
	if policy, ok := l.Gate.(SessionGrantPolicy); ok {
		c.SessionGrantKnown = true
		c.SessionGrantAllowed = policy.SessionGrantAllowed(c.Name)
	}
	if err := emit(Event{Type: PermAsk, Call: &c, Text: why, Reason: reason}); err != nil {
		return Deny, "permission question was not journaled: " + err.Error()
	}
	d := l.Human.Ask(ctx, c)
	if ctx.Err() != nil {
		d = Deny // ctrl+c must never widen permission
	}
	// Apply policy constraints: the effective decision may differ from the
	// raw click (e.g. AllowSession for bash → AllowOnce).
	effective := l.Gate.Effective(c.Name, d)
	if err := emit(Event{Type: PermReply, Call: &c, Decision: effective, RawDecision: d, Text: why, Reason: reason}); err != nil {
		return Deny, "permission reply was not journaled: " + err.Error()
	}
	if effective == AllowSession {
		l.Gate.Record(c.Name, effective)
	}
	return effective, ""
}

func checkPermission(g Gate, tool string) (Verdict, PermissionReason, string) {
	if rg, ok := g.(ReasonedGate); ok {
		v, reason, text := rg.CheckReason(tool)
		if reason != "" && !reason.Valid() {
			return VerdictDeny, PermissionReasonUnknownOrForbidden, "unknown or forbidden tool"
		}
		return v, reason, text
	}
	v, text := g.Check(tool)
	return v, "", text
}
