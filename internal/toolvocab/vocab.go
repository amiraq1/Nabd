// Package toolvocab contains the binary's stable tool vocabulary. It has no
// registry or filesystem dependency, so agent's provider-facing fence and the
// concrete tools package can share it without an import cycle.
package toolvocab

// names is immutable by API: callers only receive copies through Names.
var names = [...]string{"read_file", "write_file", "edit_file", "bash", "skill", "glob", "grep"}

// Names returns a copy of the complete tool vocabulary compiled into the
// binary. It is deliberately independent of the tools active in one session.
func Names() []string { return append([]string(nil), names[:]...) }

// Has reports whether name belongs to the binary vocabulary.
func Has(name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// IsReadOnly is fail-closed: unknown names are not read-only.
func IsReadOnly(name string) bool {
	switch name {
	case "read_file", "glob", "grep", "skill":
		return true
	default:
		return false
	}
}

// Guarded reports whether name belongs to the binary vocabulary of tools whose
// result must be projected as a structured event and must never travel as plain
// tool output. The live guard is owned by the tool layer (agent.GuardedOutcome
// through GuardedFor); this list is the binary's declaration of which names
// carry that obligation, so the agent loop can fail closed when a tool layer
// advertises a guarded name without providing the guard. It is fail-closed:
// unknown names are not guarded.
func Guarded(name string) bool {
	switch name {
	case "skill":
		return true
	default:
		return false
	}
}
