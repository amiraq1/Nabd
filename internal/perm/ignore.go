package perm

import (
	"fmt"
	"path"
	"strings"

	"nabd/internal/ignorefile"
)

// ignoredPaths is the session's ignore rule, loaded once at startup.
//
// It lives on the Policy rather than in the tools because the two questions a
// path raises are one question: "may this tool run" and "may it touch this path"
// are both permission decisions, and #128's picker-only hiding made the second
// one look answered when it was not.
type ignoredPaths struct {
	m ignorefile.Matcher
}

// SetIgnoreFile loads the session root .gitignore once. Called at startup from
// cmd/ag, before any tool runs. A missing, unreadable or symlinked file leaves
// the rule inert: a session with no ignore file refuses nothing.
func (p *Policy) SetIgnoreFile(dir string) {
	m := ignorefile.LoadDir(dir)
	p.mu.Lock()
	p.ignored = ignoredPaths{m: m}
	p.mu.Unlock()
}

// CheckRead decides whether a ReadOnly tool may touch rel, which is a
// root-relative slash-separated path. It returns Allow, or Deny with the reason
// to show and journal.
//
// The ladder, and why it is this short:
//
//  1. No ignore file, or no pattern matches → Allow. The rule is inert until a
//     project asks for it, so nothing that worked before stops working.
//  2. A pattern matches and the mode is ModeAllowReads → Allow. This is the
//     explicit override, and it is an existing documented flag whose whole
//     meaning is "reads are always allowed", so no new escape hatch is invented
//     and no operator has to discover one.
//  3. A pattern matches in every other mode → Deny, naming the pattern and the
//     override.
//
// YOLO is deliberately not consulted. YOLO is consent to change the world
// without being asked; it is not a licence to read what the project declared
// excluded, and a containment boundary that a convenience flag silently lifts is
// not a boundary. Ask is deliberately not returned either: a permission modal
// exists to approve a change, and this refusal is about disclosure, so the
// answer is a message the caller can act on rather than a prompt to click past.
func (p *Policy) CheckRead(rel string) (Verdict, string) {
	if strings.TrimSpace(rel) == "" || rel == "." {
		return Allow, ""
	}
	p.mu.Lock()
	rule := p.ignored
	mode := p.mode
	p.mu.Unlock()

	if rule.m.Empty() {
		return Allow, ""
	}
	slashed := strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	clean := path.Clean(slashed)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		// A rel that escapes the root is not this rule's question. Callers hand
		// over root-relative paths that containment (Resolve, safefs.Normalize)
		// already vetted; if one still says "..", the open itself is what must
		// refuse it, and inventing a second refusal here would only blur which
		// layer said what.
		return Allow, ""
	}
	slashed = clean
	name := slashed
	if i := strings.LastIndex(slashed, "/"); i >= 0 {
		name = slashed[i+1:]
	}
	pattern, matched := rule.m.Match(slashed, name, false)
	if !matched {
		return Allow, ""
	}
	if mode == ModeAllowReads {
		return Allow, fmt.Sprintf("allow-reads: %q is excluded by the session .gitignore (pattern %q)", slashed, pattern)
	}
	return Deny, fmt.Sprintf("%q is excluded by the session .gitignore (pattern %q); --permission-mode allow-reads reads ignored paths", slashed, pattern)
}
