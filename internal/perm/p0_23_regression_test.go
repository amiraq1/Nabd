package perm

import (
	"testing"
)

// P0-2 regression: .git and .ag contents are never readable via tools,
// regardless of .gitignore.
func TestP02_GitConfigBlocked(t *testing.T) {
	p := New(nil)
	for _, path := range []string{
		".git/config",
		".git/hooks/pre-commit",
		".GIT/CONFIG", // case-insensitive
		"sub/.git/config",
	} {
		if v, _ := p.CheckRead(path); v != Deny {
			t.Errorf("CheckRead(%q) = %v, want Deny (P0-2)", path, v)
		}
	}
}

func TestP02_AgShadowBlocked(t *testing.T) {
	p := New(nil)
	for _, path := range []string{
		".ag/shadow/abc123",
		".AG/shadow",
		"sub/.ag/file",
	} {
		if v, _ := p.CheckRead(path); v != Deny {
			t.Errorf("CheckRead(%q) = %v, want Deny (P0-2)", path, v)
		}
	}
}

// P0-3 regression: .git/hooks is never writable via tools.
func TestP03_GitHooksBlocked(t *testing.T) {
	p := New(nil)
	for _, path := range []string{
		".git/hooks/pre-commit",
		".git/config",
	} {
		if v, _ := p.CheckEdit(path); v != Deny {
			t.Errorf("CheckEdit(%q) = %v, want Deny (P0-3)", path, v)
		}
	}
}
