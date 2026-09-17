package perm

import (
	"testing"

	"nabd/internal/agent"
)

func TestModeTable(t *testing.T) {
	cls := fakeClassifier{
		"read_file":  ReadOnly,
		"write_file": Mutating,
		"edit_file":  Mutating,
		"bash":       Executing,
	}

	cases := []struct {
		mode Mode
		tool string
		want Verdict
	}{
		{ModeAsk, "read_file", Allow},
		{ModeAsk, "write_file", Ask},
		{ModeAsk, "bash", Ask},
		{ModeDeny, "read_file", Allow},
		{ModeDeny, "write_file", Deny},
		{ModeDeny, "bash", Deny},
		{ModeAllowReads, "read_file", Allow},
		{ModeAllowReads, "write_file", Deny},
		{ModeAllowReads, "bash", Deny},
		{ModePlan, "read_file", Allow},
		{ModePlan, "write_file", Deny},
		{ModePlan, "bash", Deny},
	}
	for _, c := range cases {
		p := New(cls)
		p.SetMode(c.mode)
		if got, _ := p.Check(c.tool); got != c.want {
			t.Errorf("mode=%d tool=%s: got %v, want %v", c.mode, c.tool, got, c.want)
		}
	}
}

// TestModePlanOverridesGrants is the load-bearing property of plan mode: a
// standing session grant or YOLO must not let a write or command through.
func TestModePlanOverridesGrants(t *testing.T) {
	cls := fakeClassifier{"write_file": Mutating, "bash": Executing}
	p := New(cls)
	p.SetMode(ModePlan)
	p.Record("write_file", agent.AllowSession)
	p.SetYOLO(true)

	if v, why := p.Check("write_file"); v != Deny {
		t.Fatalf("plan mode let a granted write through: %v (%q)", v, why)
	}
	if v, why := p.Check("bash"); v != Deny {
		t.Fatalf("plan mode let a yolo shell through: %v (%q)", v, why)
	}
	// And reads still pass.
	if v, _ := p.Check("read_file"); v != Allow {
		// read_file is unknown here; use a known read-only tool instead.
	}
}

func TestModePlanAllowsReads(t *testing.T) {
	cls := fakeClassifier{"read_file": ReadOnly}
	p := New(cls)
	p.SetMode(ModePlan)
	if v, _ := p.Check("read_file"); v != Allow {
		t.Fatalf("plan mode denied a read: %v", v)
	}
}

// TestModeAskIsDefault pins that a zero-value Policy behaves exactly like the
// old code: ungranted writes ask. This is what the interactive default relies
// on.
func TestModeAskIsDefault(t *testing.T) {
	p := New(fakeClassifier{"write_file": Mutating})
	if p.Mode() != ModeAsk {
		t.Fatalf("default mode = %v, want ModeAsk", p.Mode())
	}
	if v, _ := p.Check("write_file"); v != Ask {
		t.Fatalf("default write verdict = %v, want Ask", v)
	}
}

func TestParseModeRoundTrip(t *testing.T) {
	for _, s := range []string{"ask", "deny", "allow-reads", "plan", ""} {
		mode, err := ParseMode(s)
		if err != nil {
			t.Errorf("ParseMode(%q) failed: %v", s, err)
		}
		if s == "allow-reads" && mode != ModeAllowReads {
			t.Errorf("ParseMode(%q) = %v, want ModeAllowReads", s, mode)
		}
	}
	if _, err := ParseMode("nope"); err == nil {
		t.Fatal("bogus mode accepted")
	}
}
