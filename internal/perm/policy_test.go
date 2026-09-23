package perm

import (
	"fmt"
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
	p.SetYOLO(true)
	p.Reset()
	if v, _ := p.Check("write_file"); v != Ask {
		t.Error("Reset did not revoke grant and YOLO")
	}
	if p.YOLO() {
		t.Error("Reset left YOLO enabled")
	}
}

func TestYOLOIsBoundedButNotEternal(t *testing.T) {
	p := New(testCls())
	p.SetYOLO(true)
	if v, _ := p.Check("write_file"); v != Allow {
		t.Errorf("yolo write_file = %v, want Allow", v)
	}
	if v, why := p.Check("bash"); v != Ask {
		t.Errorf("yolo bash = %v (%q), want Ask (executing is never auto-approved)", v, why)
	}
	if v, _ := p.Check("nope"); v != Deny {
		t.Error("yolo allowed an unknown tool")
	}
	p.SetYOLO(false)
	if v, _ := p.Check("bash"); v != Ask {
		t.Error("yolo outlived its switch")
	}
}

// TestYOLOVerdictMatrix is the full cross-product of class, YOLO and mode
// (no standing grants). It is the table the ladder comment describes, and it
// pins that YOLO only ever widens the verdict for Mutating calls.
func TestYOLOVerdictMatrix(t *testing.T) {
	tools := []struct {
		name string
		cls  Class
	}{
		{"read_file", ReadOnly},
		{"write_file", Mutating},
		{"bash", Executing},
	}
	modes := []struct {
		name string
		mode Mode
	}{
		{"ModeAsk", ModeAsk},
		{"ModeDeny", ModeDeny},
		{"ModePlan", ModePlan},
	}
	want := func(class Class, yolo bool, mode Mode) Verdict {
		if class == ReadOnly {
			return Allow
		}
		if mode == ModePlan {
			return Deny
		}
		if yolo && class == Mutating {
			return Allow
		}
		if mode == ModeDeny || mode == ModeAllowReads {
			return Deny
		}
		return Ask
	}

	for _, tl := range tools {
		for _, yolo := range []bool{false, true} {
			for _, mm := range modes {
				t.Run(fmt.Sprintf("%s/yolo=%v/%s", tl.name, yolo, mm.name), func(t *testing.T) {
					p := New(testCls())
					p.SetMode(mm.mode)
					p.SetYOLO(yolo)
					got, why := p.Check(tl.name)
					if got != want(tl.cls, yolo, mm.mode) {
						t.Fatalf("Check(%s) = %v (%q), want %v", tl.name, got, why, want(tl.cls, yolo, mm.mode))
					}
				})
			}
		}
	}
}

// TestYOLODoesNotBypassExecuting is the load-bearing property of the enforce
// decision: YOLO is consent to change the world, not to run arbitrary code.
// Under ModeAsk the shell still stops to ask; under ModeDeny it is denied.
func TestYOLODoesNotBypassExecuting(t *testing.T) {
	p := New(testCls())
	p.SetYOLO(true)

	p.SetMode(ModeAsk)
	if v, why := p.Check("bash"); v != Ask {
		t.Fatalf("YOLO + ModeAsk bash = %v (%q), want Ask", v, why)
	}
	p.SetMode(ModeDeny)
	if v, why := p.Check("bash"); v != Deny {
		t.Fatalf("YOLO + ModeDeny bash = %v (%q), want Deny", v, why)
	}
	p.SetMode(ModePlan)
	if v, why := p.Check("bash"); v != Deny {
		t.Fatalf("YOLO + ModePlan bash = %v (%q), want Deny", v, why)
	}
}

// TestYOLOStillAllowsMutating pins that the enforce change did not remove the
// convenience YOLO exists for: writes still run without a prompt.
func TestYOLOStillAllowsMutating(t *testing.T) {
	p := New(testCls())
	p.SetYOLO(true)
	p.SetMode(ModeAsk)
	for _, tool := range []string{"write_file", "edit_file"} {
		if v, why := p.Check(tool); v != Allow {
			t.Fatalf("YOLO %s = %v (%q), want Allow", tool, v, why)
		}
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

func TestSessionGrantAllowedOnlyForMutatingTools(t *testing.T) {
	p := New(testCls())
	if p.SessionGrantAllowed("read_file") {
		t.Fatal("read-only tool must not expose a standing grant")
	}
	if !p.SessionGrantAllowed("write_file") {
		t.Fatal("mutating tool should expose a standing grant")
	}
	if p.SessionGrantAllowed("bash") {
		t.Fatal("executing tool must not expose a standing grant")
	}
}
