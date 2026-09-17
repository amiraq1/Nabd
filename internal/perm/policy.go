// Package perm decides whether a tool may run. Three outcomes, not
// thirteen: allow, ask, deny. A ladder nobody can hold in their head is
// a ladder nobody audits.
package perm

import (
	"fmt"
	"strings"
	"sync"

	"nabd/internal/agent"
)

type Verdict uint8

const (
	// Ask is the zero value on purpose: a tool nobody classified is a
	// tool nobody vouched for, and it stops to ask.
	Ask Verdict = iota
	Allow
	Deny
)

// Mode is the permission policy the session runs under. It lives in perm,
// not in the UI, so the same rule applies whether the human answers through
// a TUI or the run is headless. The zero value is ModeAsk, which is the
// current interactive behaviour (every ungranted Mutating/Executing tool
// stops to ask).
type Mode uint8

const (
	// ModeAsk stops for an answer on every ungranted write or command.
	ModeAsk Mode = iota
	// ModeDeny answers "deny" to every ungranted write or command, so the
	// run never waits and never changes a byte.
	ModeDeny
	// ModeAllowReads is kept for compatibility: it preserves the explicit
	// read override for session .gitignore paths.
	ModeAllowReads
	// ModePlan is strict read-only: reads pass, every Mutating and
	// Executing call is denied regardless of standing grants or YOLO. A
	// session in plan mode can inspect the tree but never change it.
	ModePlan
)

// ParseMode maps the CLI string to a Mode. The empty string means "ask",
// which is the interactive default and lets the caller apply per-path
// defaults without the policy knowing about the CLI.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "", "ask":
		return ModeAsk, nil
	case "deny":
		return ModeDeny, nil
	case "allow-reads":
		return ModeAllowReads, nil
	case "plan":
		return ModePlan, nil
	default:
		return ModeAsk, fmt.Errorf("unknown permission-mode %q (want ask|deny|allow-reads|plan)", s)
	}
}

// Class is what a tool does to the world, declared by the tool itself.
type Class uint8

const (
	ReadOnly  Class = iota // cannot change a byte
	Mutating               // writes, edits, deletes
	Executing              // runs arbitrary code
)

// Classifier is implemented by the registry: name -> class.
type Classifier interface {
	Class(tool string) (Class, bool)
}

// Policy holds the session's standing grants. It is consulted before a
// tool runs and updated after the user answers.
//
// Grants are per tool name, never per argument. "Allow write_file for
// this session" is a sentence a human can evaluate; "allow write_file
// when the path matches this glob" is one they cannot, and the appearance
// of precision there is worse than none.
type Policy struct {
	mu      sync.Mutex
	cls     Classifier
	granted map[string]bool
	yolo    bool
	mode    Mode
	// ignored is the session ignore rule (see ignore.go). It is part of the
	// policy, not of a tool: the same patterns must answer for the picker, for
	// read_file and for grep.
	ignored ignoredPaths
}

func New(cls Classifier) *Policy {
	return &Policy{cls: cls, granted: map[string]bool{}}
}

// SetMode sets the permission policy the session runs under. It is set by the
// CLI after construction, so a Policy can be built once and reused across
// modes.
func (p *Policy) SetMode(m Mode) {
	p.mu.Lock()
	p.mode = m
	p.mu.Unlock()
}

func (p *Policy) Mode() Mode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mode
}

// SetYOLO disables asking. It exists because it will be demanded; it is
// never persisted, never the default, and every grant it implies dies
// with the process.
func (p *Policy) SetYOLO(on bool) {
	p.mu.Lock()
	p.yolo = on
	p.mu.Unlock()
}

func (p *Policy) YOLO() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.yolo
}

// Check returns the verdict for one call, plus a short reason to show.
//
// The ladder, highest priority first:
//  1. No name, unknown tool → Deny.
//  2. ReadOnly → Allow in every mode, including plan.
//  3. ModePlan → Deny for Mutating/Execuring, ignoring YOLO and standing
//     grants. Plan mode is read-only, full stop.
//  4. YOLO → Allow (plan already returned).
//  5. Standing session grant → Allow.
//  6. ModeDeny / ModeAllowReads → Deny (never wait).
//  7. Otherwise → Ask.
func (p *Policy) Check(tool string) (Verdict, string) {
	if strings.TrimSpace(tool) == "" {
		return Deny, "tool with no name"
	}

	class, known := p.cls.Class(tool)
	if !known {
		return Deny, "unknown tool"
	}
	if class == ReadOnly {
		return Allow, ""
	}

	p.mu.Lock()
	mode := p.mode
	yolo := p.yolo
	granted := p.granted[tool]
	p.mu.Unlock()

	if mode == ModePlan {
		return Deny, "plan mode: read-only"
	}
	if yolo {
		return Allow, ""
	}
	if class == Mutating && granted {
		return Allow, "مسموح لهذه الجلسة"
	}
	if mode == ModeDeny || mode == ModeAllowReads {
		return Deny, "denied by policy"
	}
	return Ask, ""
}

// Record applies the user's answer. Only Mutating tools can leave a
// standing grant behind; a session-wide yes to a shell is dropped to a
// one-time yes, silently and by design.
func (p *Policy) Record(tool string, d agent.Decision) {
	if d != agent.AllowSession {
		return
	}
	class, known := p.cls.Class(tool)
	if !known || class != Mutating {
		return
	}
	p.mu.Lock()
	p.granted[tool] = true
	p.mu.Unlock()
}

// Effective reports what a decision actually means once the policy has
// had its say, so the journal records the grant that was given rather
// than the one that was clicked.
func (p *Policy) Effective(tool string, d agent.Decision) agent.Decision {
	if d != agent.AllowSession {
		return d
	}
	if class, known := p.cls.Class(tool); !known || class != Mutating {
		return agent.AllowOnce
	}
	return d
}

// SessionGrantAllowed reports whether a standing grant is meaningful for the
// tool. Executing tools are deliberately excluded; the UI must not offer a
// choice that the policy will silently downgrade.
func (p *Policy) SessionGrantAllowed(tool string) bool {
	class, known := p.cls.Class(tool)
	return known && class == Mutating
}

// Reset clears standing grants. Called when the working directory or the
// conversation changes: consent is to a situation, not to a name.
func (p *Policy) Reset() {
	p.mu.Lock()
	p.granted = map[string]bool{}
	p.yolo = false
	p.mu.Unlock()
}
