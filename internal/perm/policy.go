// Package perm decides whether a tool may run. Three outcomes, not
// thirteen: allow, ask, deny. A ladder nobody can hold in their head is
// a ladder nobody audits.
package perm

import (
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
}

func New(cls Classifier) *Policy {
	return &Policy{cls: cls, granted: map[string]bool{}}
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
	defer p.mu.Unlock()
	if p.yolo {
		return Allow, ""
	}
	// Executing is deliberately excluded from session grants. A blanket
	// yes to write_file risks a file; a blanket yes to a shell risks the
	// machine, and the second command is never the one you approved.
	if class == Mutating && p.granted[tool] {
		return Allow, "مسموح لهذه الجلسة"
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

// Reset clears standing grants. Called when the working directory or the
// conversation changes: consent is to a situation, not to a name.
func (p *Policy) Reset() {
	p.mu.Lock()
	p.granted = map[string]bool{}
	p.mu.Unlock()
}
