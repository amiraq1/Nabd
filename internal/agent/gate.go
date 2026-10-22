// Package agent: gate.go is the only place a tool call can be stopped
// before it runs. The loop never inspects arguments and never guesses.
package agent

import (
	"context"
	"nabd/internal/event"
)

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
	Record(tool string, d event.Decision)
	Effective(tool string, d event.Decision) event.Decision
}

// ReasonedGate is the additive permission-reason contract. Keeping it optional
// preserves source compatibility for embedders and test gates while the built-in
// policy emits stable codes for every new permission event.
type ReasonedGate interface {
	CheckReason(tool string) (Verdict, event.PermissionReason, string)
}

// SessionGrantPolicy is optional so existing test gates and integrations keep
// compiling. The UI uses it only when the policy can explicitly describe
// whether an AllowSession choice is valid for this tool.
type SessionGrantPolicy interface {
	SessionGrantAllowed(tool string) bool
}

// CallGrantPolicy is the optional per-call extension of SessionGrantPolicy: a
// gate that answers per call can also say whether this call may carry a
// standing grant. decide consults it so a call that must be approved every time
// neither offers the session choice nor records one — which is what makes
// "approval required on every write" true rather than merely displayed.
type CallGrantPolicy interface {
	SessionGrantAllowedCall(c event.ToolCall) bool
}

// CallAwareGate is an optional gate extension for tools whose permission
// verdict depends on the call arguments, not just the tool name
// (MCP-SEC-09: one adapter name fronts many server tools with different
// local classifications). When the gate implements it, decide consults
// CheckCall for the calls HandlesCall accepts and keeps the name-based
// Check for everything else. RecordCall runs after a handled call is
// allowed, so per-call grants (e.g. an MCP first-approval) are persisted
// exactly once per decision. Implementations must not block on the loop;
// decide is sequential, so no extra synchronization is required.
type CallAwareGate interface {
	HandlesCall(c event.ToolCall) bool
	CheckCall(c event.ToolCall) (Verdict, event.PermissionReason, string)
	RecordCall(c event.ToolCall, d event.Decision)
}

// Asker is the human, reached through the UI. It must return on ctx death.
type Asker interface {
	Ask(ctx context.Context, call event.ToolCall) event.Decision
}

// decide emits the question and the answer into the journal, so a replay
// shows not only what ran but what was permitted, and by whom. The
// PermReply event records both the raw user decision and the effective
// decision actually applied after policy constraints (e.g. AllowSession
// for an Executing tool becomes AllowOnce).
func (l *Loop) decide(ctx context.Context, c event.ToolCall, emit func(event.Event) error) (event.Decision, string) {
	if l.Gate == nil {
		return event.Deny, "no permission gate installed"
	}
	// Per-call verdicts for argument-addressed tools (MCP). The PermAsk /
	// PermReply journaling and the human prompt below are shared with the
	// name-based path; only the verdict source and the post-allow recording
	// differ.
	var cg CallAwareGate
	v, reason, why := checkPermission(l.Gate, c.Name)
	if tmp, ok := l.Gate.(CallAwareGate); ok && tmp.HandlesCall(c) {
		cg = tmp
		v, reason, why = cg.CheckCall(c)
	}
	switch v {
	case VerdictAllow:
		if cg != nil {
			cg.RecordCall(c, event.AllowOnce)
		}
		return event.AllowOnce, ""
	case VerdictDeny:
		if why == "" {
			why = "unknown or forbidden tool"
			reason = event.PermissionReasonUnknownOrForbidden
		}
		if err := emit(event.Event{Type: event.PermReply, Call: &c, Decision: event.Deny, RawDecision: event.Deny, Text: why, Reason: reason}); err != nil {
			return event.Deny, "permission decision was not journaled: " + err.Error()
		}
		return event.Deny, why
	}
	if l.Human == nil {
		const noPrompt = "no prompt interface"
		if err := emit(event.Event{Type: event.PermReply, Call: &c, Decision: event.Deny, RawDecision: event.Deny, Text: noPrompt, Reason: event.PermissionReasonNoPrompt}); err != nil {
			return event.Deny, "permission decision was not journaled: " + err.Error()
		}
		return event.Deny, noPrompt
	}
	if policy, ok := l.Gate.(SessionGrantPolicy); ok {
		c.SessionGrantKnown = true
		c.SessionGrantAllowed = policy.SessionGrantAllowed(c.Name)
	}
	if cgp, ok := l.Gate.(CallGrantPolicy); ok && !cgp.SessionGrantAllowedCall(c) {
		c.SessionGrantAllowed = false
	}
	if err := emit(event.Event{Type: event.PermAsk, Call: &c, Text: why, Reason: reason}); err != nil {
		return event.Deny, "permission question was not journaled: " + err.Error()
	}
	d := l.Human.Ask(ctx, c)
	if ctx.Err() != nil {
		d = event.Deny // ctrl+c must never widen permission
	}
	// Apply policy constraints: the effective decision may differ from the
	// raw click (e.g. AllowSession for bash → AllowOnce).
	effective := l.Gate.Effective(c.Name, d)
	if cgp, ok := l.Gate.(CallGrantPolicy); ok && effective == event.AllowSession && !cgp.SessionGrantAllowedCall(c) {
		// Defence in depth: an asker that answers AllowSession anyway (a test
		// double, an embedder) gets one-time consent, never a standing grant.
		effective = event.AllowOnce
	}
	if err := emit(event.Event{Type: event.PermReply, Call: &c, Decision: effective, RawDecision: d, Text: why, Reason: reason}); err != nil {
		return event.Deny, "permission reply was not journaled: " + err.Error()
	}
	if effective == event.AllowSession {
		l.Gate.Record(c.Name, effective)
	}
	if cg != nil && effective == event.AllowOnce {
		cg.RecordCall(c, effective)
	}
	return effective, ""
}

func checkPermission(g Gate, tool string) (Verdict, event.PermissionReason, string) {
	if rg, ok := g.(ReasonedGate); ok {
		v, reason, text := rg.CheckReason(tool)
		if reason != "" && !reason.Valid() {
			return VerdictDeny, event.PermissionReasonUnknownOrForbidden, "unknown or forbidden tool"
		}
		return v, reason, text
	}
	v, text := g.Check(tool)
	return v, "", text
}
