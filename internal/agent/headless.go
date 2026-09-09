// Package agent: headless.go provides a non-interactive Asker and a
// permission-mode Gate for headless execution (-p). In headless mode there
// is no human at a terminal, so Ask must never block and never read a tty.
//
// The zero value of Decision is Deny. headlessAsker returns Deny for every
// call — that is the literal behaviour the house rules demand, not a
// spiritual one. The permission mode (deny/allow-reads/ask) is applied at the
// Gate layer, which converts VerdictAsk to VerdictDeny before the Asker is
// ever reached (unless mode is PermModeAsk, in which case the Asker denies).
package agent

import "context"

// PermissionMode controls how headless mode resolves tools that would
// otherwise prompt a human.
type PermissionMode int

const (
	// PermModeDeny denies every tool that would ask. ReadOnly tools still
	// auto-allow. This is the default: safe for CI, useless for anything
	// that needs to write.
	PermModeDeny PermissionMode = iota
	// PermModeAllowReads allows ReadOnly tools and denies the rest.
	// Functionally identical to PermModeDeny given the policy ladder
	// (ReadOnly already auto-allows), but documents intent: "read only".
	PermModeAllowReads
	// PermModeAsk keeps VerdictAsk at the Gate. The headlessAsker then
	// denies — there is no human to ask. Exists so callers can pass the
	// same flag set they use interactively and get a clean denial instead
	// of a hang.
	PermModeAsk
)

// HeadlessAsker never blocks and never reads a tty. It returns Deny for
// every call. ReadOnly tools never reach here: the Gate auto-allows them.
func (HeadlessAsker) Ask(ctx context.Context, call ToolCall) Decision {
	return Deny
}

// HeadlessAsker is the non-interactive Asker for headless mode (-p).
type HeadlessAsker struct{}

// HeadlessGate wraps a policy Gate and applies the permission mode:
// any VerdictAsk that is not explicitly PermModeAsk becomes VerdictDeny.
// This is the only place the mode is enforced, so the policy ladder in
// perm.Policy stays the single source of truth for tool classification.
type HeadlessGate struct {
	Inner Gate
	Mode  PermissionMode
}

func (g HeadlessGate) Check(tool string) (Verdict, string) {
	v, why := g.Inner.Check(tool)
	if v == VerdictAsk && g.Mode != PermModeAsk {
		return VerdictDeny, "headless: " + why
	}
	return v, why
}

func (g HeadlessGate) Record(tool string, d Decision) {
	g.Inner.Record(tool, d)
}

func (g HeadlessGate) Effective(tool string, d Decision) Decision {
	return g.Inner.Effective(tool, d)
}

// Classify/IsNetwork/Offline are exposed only when the inner Gate implements
// them, so the loop's optional-interface assertion finds them through the
// HeadlessGate wrapper. Headless mode otherwise ignores risk (no human to
// warn) but still honours the offline kill switch.
func (g HeadlessGate) Classify(cmd string) RiskClass {
	if rc, ok := g.Inner.(riskClassifier); ok {
		return rc.Classify(cmd)
	}
	return RiskClass{Level: RiskLow}
}

func (g HeadlessGate) IsNetwork(cmd string) bool {
	if rc, ok := g.Inner.(riskClassifier); ok {
		return rc.IsNetwork(cmd)
	}
	return false
}

func (g HeadlessGate) Offline() bool {
	if rc, ok := g.Inner.(riskClassifier); ok {
		return rc.Offline()
	}
	return false
}
