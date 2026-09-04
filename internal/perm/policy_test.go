package perm

import (
	"testing"

	"nabd/internal/agent"
)

// fakeClassifier implements Classifier for unit-level policy tests.
type fakeClassifier map[string]Class

func (f fakeClassifier) Class(n string) (Class, bool) { c, ok := f[n]; return c, ok }

func testCls() fakeClassifier {
	return fakeClassifier{
		"read_file":  ReadOnly,
		"write_file": Mutating,
		"edit_file":  Mutating,
		"glob":       ReadOnly,
		"grep":       ReadOnly,
		"bash":       Executing,
	}
}

func TestReadIsFreeWritesAsk(t *testing.T) {
	p := New(testCls())
	for _, tool := range []string{"read_file", "glob", "grep"} {
		if v, _ := p.Check(tool); v != Allow {
			t.Errorf("%s = %v, want Allow", tool, v)
		}
	}
	for _, tool := range []string{"write_file", "edit_file", "bash"} {
		if v, _ := p.Check(tool); v != Ask {
			t.Errorf("%s = %v, want Ask", tool, v)
		}
	}
}

func TestUnknownToolIsDenied(t *testing.T) {
	p := New(testCls())
	if v, _ := p.Check("rm_rf"); v != Deny {
		t.Errorf("unknown tool = %v, want Deny", v)
	}
	if v, _ := p.Check("  "); v != Deny {
		t.Errorf("empty name = %v, want Deny", v)
	}
	p.Record("rm_rf", agent.AllowSession)
	if v, _ := p.Check("rm_rf"); v != Deny {
		t.Error("granting an unknown tool made it allowed")
	}
}

func TestSessionGrantAppliesToWritesOnly(t *testing.T) {
	p := New(testCls())

	p.Record("write_file", agent.AllowSession)
	if v, _ := p.Check("write_file"); v != Allow {
		t.Errorf("granted write_file = %v, want Allow", v)
	}
	// edit_file is a known Mutating tool but was NOT granted: it must ask,
	// not be denied and not be allowed (no leak).
	if v, _ := p.Check("edit_file"); v != Ask {
		t.Errorf("edit_file = %v after granting write_file; want Ask (no leak)", v)
	}

	// bash can never be granted for a session.
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
	p := New(testCls())
	for _, d := range []agent.Decision{agent.Deny, agent.AllowOnce} {
		p.Record("write_file", d)
		if v, _ := p.Check("write_file"); v != Ask {
			t.Errorf("after %v the tool is %v, want Ask", d, v)
		}
	}
}

func TestResetRevokes(t *testing.T) {
	p := New(testCls())
	p.Record("write_file", agent.AllowSession)
	p.Reset()
	if v, _ := p.Check("write_file"); v != Ask {
		t.Error("Reset did not revoke")
	}
}

func TestYOLOIsTotalButNotEternal(t *testing.T) {
	p := New(testCls())
	p.SetYOLO(true)
	for _, tool := range []string{"write_file", "bash"} {
		if v, _ := p.Check(tool); v != Allow {
			t.Errorf("yolo %s = %v, want Allow", tool, v)
		}
	}
	if v, _ := p.Check("nope"); v != Deny {
		t.Error("yolo allowed an unknown tool")
	}
	p.SetYOLO(false)
	if v, _ := p.Check("bash"); v != Ask {
		t.Error("yolo outlived its switch")
	}
}

func TestRawDecisionForBash(t *testing.T) {
	p := New(testCls())
	if got := p.Effective("bash", agent.AllowSession); got != agent.AllowOnce {
		t.Errorf("Effective(bash, AllowSession) = %v, want AllowOnce", got)
	}
	if got := p.Effective("bash", agent.AllowOnce); got != agent.AllowOnce {
		t.Errorf("Effective(bash, AllowOnce) = %v, want AllowOnce", got)
	}
	if got := p.Effective("bash", agent.Deny); got != agent.Deny {
		t.Errorf("Effective(bash, Deny) = %v, want Deny", got)
	}
}

func TestRawDecisionForWriteFile(t *testing.T) {
	p := New(testCls())
	if got := p.Effective("write_file", agent.AllowSession); got != agent.AllowSession {
		t.Errorf("Effective(write_file, AllowSession) = %v, want AllowSession", got)
	}
}
