package perm

import (
	"testing"

	"nabd/internal/agent"
)

type fake map[string]Class

func (f fake) Class(n string) (Class, bool) { c, ok := f[n]; return c, ok }

func cls() fake {
	return fake{"read_file": ReadOnly, "write_file": Mutating, "bash": Executing}
}

func TestReadIsFreeWritesAsk(t *testing.T) {
	p := New(cls())
	if v, _ := p.Check("read_file"); v != Allow {
		t.Errorf("read_file = %v, want Allow", v)
	}
	for _, tool := range []string{"write_file", "bash"} {
		if v, _ := p.Check(tool); v != Ask {
			t.Errorf("%s = %v, want Ask", tool, v)
		}
	}
}

func TestUnknownToolIsDenied(t *testing.T) {
	p := New(cls())
	if v, _ := p.Check("rm_rf"); v != Deny {
		t.Errorf("unknown tool = %v, want Deny", v)
	}
	if v, _ := p.Check("  "); v != Deny {
		t.Errorf("empty name = %v, want Deny", v)
	}
	// A grant for a tool that does not exist must not create one.
	p.Record("rm_rf", agent.AllowSession)
	if v, _ := p.Check("rm_rf"); v != Deny {
		t.Error("granting an unknown tool made it allowed")
	}
}

func TestSessionGrantAppliesToWritesOnly(t *testing.T) {
	p := New(cls())

	p.Record("write_file", agent.AllowSession)
	if v, _ := p.Check("write_file"); v != Allow {
		t.Errorf("granted write_file = %v, want Allow", v)
	}
	if v, _ := p.Check("edit_file"); v != Deny {
		t.Error("a grant leaked to another tool")
	}

	// The whole point: a shell can never be granted for a session.
	p.Record("bash", agent.AllowSession)
	if v, _ := p.Check("bash"); v != Ask {
		t.Errorf("bash = %v after session grant, want Ask forever", v)
	}
	if got := p.Effective("bash", agent.AllowSession); got != agent.AllowOnce {
		t.Errorf("Effective(bash, session) = %v, want once", got)
	}
	if got := p.Effective("write_file", agent.AllowSession); got != agent.AllowSession {
		t.Errorf("Effective(write_file, session) = %v, want session", got)
	}
}

func TestOnceAndDenyLeaveNothingBehind(t *testing.T) {
	p := New(cls())
	for _, d := range []agent.Decision{agent.Deny, agent.AllowOnce} {
		p.Record("write_file", d)
		if v, _ := p.Check("write_file"); v != Ask {
			t.Errorf("after %v the tool is %v, want Ask", d, v)
		}
	}
}

func TestResetRevokes(t *testing.T) {
	p := New(cls())
	p.Record("write_file", agent.AllowSession)
	p.Reset()
	if v, _ := p.Check("write_file"); v != Ask {
		t.Error("Reset did not revoke")
	}
}

func TestYOLOIsTotalButNotEternal(t *testing.T) {
	p := New(cls())
	p.SetYOLO(true)
	for _, tool := range []string{"write_file", "bash"} {
		if v, _ := p.Check(tool); v != Allow {
			t.Errorf("yolo %s = %v, want Allow", tool, v)
		}
	}
	// Even yolo cannot invent a tool.
	if v, _ := p.Check("nope"); v != Deny {
		t.Error("yolo allowed an unknown tool")
	}
	p.SetYOLO(false)
	if v, _ := p.Check("bash"); v != Ask {
		t.Error("yolo outlived its switch")
	}
}
