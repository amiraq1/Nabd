package perm_test

import (
	"testing"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

// TestRegistryClassificationUsesRealRegistry is an integration test that uses
// the actual tools.Registry (not a fake classifier) to verify that tools are
// classified correctly — including bash as Executing — and that the policy
// applies the correct verdict and effective decision for each class.
func TestRegistryClassificationUsesRealRegistry(t *testing.T) {
	dir := t.TempDir()
	root, err := tools.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(root, sh)
	p := perm.New(reg)

	// read-only tools are allowed without asking.
	for _, tool := range []string{"read_file", "glob", "grep"} {
		if v, _ := p.Check(tool); v != perm.Allow {
			t.Errorf("%s = %v, want Allow", tool, v)
		}
	}

	// mutating tools ask, then allow with session grant.
	for _, tool := range []string{"write_file", "edit_file"} {
		if v, _ := p.Check(tool); v != perm.Ask {
			t.Errorf("%s = %v, want Ask", tool, v)
		}
		p.Record(tool, agent.AllowSession)
		if v, _ := p.Check(tool); v != perm.Allow {
			t.Errorf("%s = %v after session grant, want Allow", tool, v)
		}
	}

	// bash is Executing: it asks, and NEVER accepts a session grant.
	if v, _ := p.Check("bash"); v != perm.Ask {
		t.Errorf("bash = %v, want Ask", v)
	}
	p.Record("bash", agent.AllowSession)
	if v, _ := p.Check("bash"); v != perm.Ask {
		t.Errorf("bash = %v after session grant; must stay Ask", v)
	}
	if got := p.Effective("bash", agent.AllowSession); got != agent.AllowOnce {
		t.Errorf("Effective(bash, session) = %v, want AllowOnce", got)
	}

	// unknown tools are denied even with a grant.
	p.Record("unknown_tool", agent.AllowSession)
	if v, _ := p.Check("unknown_tool"); v != perm.Deny {
		t.Errorf("unknown tool = %v, want Deny", v)
	}
}
