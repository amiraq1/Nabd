// Package toolvocab contains the binary's stable tool vocabulary. It has no
// registry or filesystem dependency, so agent's provider-facing fence and the
// concrete tools package can share it without an import cycle.
package toolvocab

var Names = []string{"read_file", "write_file", "edit_file", "bash", "skill", "glob", "grep"}

var ReadOnly = map[string]bool{
	"read_file": true,
	"glob":      true,
	"grep":      true,
	"skill":     true,
}

func IsReadOnly(name string) bool { return ReadOnly[name] }
