package main

import (
	"fmt"
	"strings"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
	"nabd/internal/ui"
)

// interactiveSession is the part of an interactive run that must be built
// identically by both TUIs: the workspace root, the tool registry, the
// permission policy, the approver, the agent loop, and the slash-command
// callbacks. Chat and Feed differ only in how they render and where the
// events go, never in what the loop is or what a command does. Building it
// once here is what keeps the two paths from drifting; the previous shape
// wired the same five callbacks and the same gate twice, by hand.
type interactiveSession struct {
	root *tools.Root
	reg  *tools.Registry
	pol  *perm.Policy
	ap   *ui.Approver
	loop *agent.Loop
}

// newInteractiveSession builds the shared core. It deliberately does not
// open a journal or wire a sink: the journal and the view are the caller's
// concern, and they are the only things the two entry points legitimately
// differ on.
func newInteractiveSession(prov provider.Provider) (*interactiveSession, error) {
	root, err := tools.NewRoot("")
	if err != nil {
		return nil, err
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		return nil, err
	}
	reg := tools.NewRegistry(root, sh)
	pol := perm.New(reg)
	wirePathRule(root, reg, pol)
	ap := ui.NewApprover()
	loop := newSessionLoop(prov, reg, gate{pol}, ap)
	return &interactiveSession{root: root, reg: reg, pol: pol, ap: ap, loop: loop}, nil
}

// wirePathRule installs the session ignore rule on the policy and hands the same
// policy to the registry as its path gate. Both entry points call it, and both
// pass the same *perm.Policy they use as the gate, so a path and a tool name can
// never be judged by two different rules.
//
// This is the whole wiring: if it is missing, the picker still hides ignored
// paths and read_file still hands them over, which is exactly the cosmetic
// boundary this replaces.
func wirePathRule(root *tools.Root, reg *tools.Registry, pol *perm.Policy) {
	pol.SetIgnoreFile(root.Dir())
	reg.SetPathGate(pol)
}

// SetMode applies the permission policy to the built session. It is called by
// the CLI after construction, so the same builder works in every mode.
func (s *interactiveSession) SetMode(m perm.Mode) {
	s.pol.SetMode(m)
}

// callbacks builds the slash-command hooks once. The command bodies live in
// named helpers so Chat and Feed cannot diverge, and so the /rewind contract
// is one signature instead of the two it used to be.
func (s *interactiveSession) callbacks() *ui.SessionCallbacks {
	return &ui.SessionCallbacks{
		OnUndo:    func(n int) string { return fileUndo(s.loop, s.reg, n) },
		OnCompact: func() string { return chatOnCompact(s.loop) },
		OnRewind:  func(n int) (string, string) { return rewindSummary(s.loop, n) },
		OnCtx:     func() string { return ctxSummary(s.loop) },
		OnEdits:   func() string { return editsSummary(s.loop) },
	}
}

// rewindSummary cuts n turns and returns the restored text for the composer
// plus the status line. The restored text is what the cut removed; it is
// handed back rather than set on the view so both views apply it their own
// way.
func rewindSummary(loop *agent.Loop, n int) (string, string) {
	if loop == nil {
		return "", "rewind not supported"
	}
	txt, err := loop.Rewind(n)
	if err != nil {
		return "", err.Error()
	}
	return txt, fmt.Sprintf("rewound %d turns · disk edits remain, /undo does not cover edits after branch cut", n)
}

// ctxSummary reports the current context pressure and budget.
func ctxSummary(loop *agent.Loop) string {
	ms := agent.Squeeze(agent.Messages(agent.Live(loop.Hist())), agent.KeepFullRounds)
	p := loop.Budget.Pressure(ms)
	return fmt.Sprintf("context %d%% (%d / %d tokens)", int(p*100), loop.Budget.Estimate(ms), loop.Budget.Usable())
}

// editsSummary lists the edits that /undo can still reverse.
func editsSummary(loop *agent.Loop) string {
	p := editRecords(agent.Live(loop.Hist()))
	if len(p) == 0 {
		return "no reversible edits pending"
	}
	var b strings.Builder
	for i, e := range p {
		tool := "edit_file"
		if e.Patch == "" {
			tool = "write_file"
		}
		fmt.Fprintf(&b, "%d· %s %s\n", i+1, tool, e.Path)
	}
	return strings.TrimRight(b.String(), "\n")
}
