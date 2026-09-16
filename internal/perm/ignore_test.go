package perm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeIgnoreFile puts a .gitignore with the given patterns into dir, so a test
// controls both the project and its ignore declaration.
func writeIgnoreFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSetIgnoreFileEmptyDirLeavesRuleInert pins the inert-until-declared
// property: no ignore file, no refusals, no mode surprises.
func TestSetIgnoreFileEmptyDirLeavesRuleInert(t *testing.T) {
	p := New(testCls())
	p.SetIgnoreFile(t.TempDir())
	for _, mode := range []Mode{ModeAsk, ModeDeny, ModeAllowReads, ModePlan} {
		p.SetMode(mode)
		if v, why := p.CheckRead("secrets.env"); v != Allow || why != "" {
			t.Fatalf("mode %d: no ignore file must mean Allow, got %v %q", mode, v, why)
		}
	}
}

// TestCheckReadRefusesInAskAndDenyAndPlan pins the declared mode-scope decision:
// the block is default in every permission mode except allow-reads. YOLO is
// deliberately absent from this list — it cannot lift an ignore rule, by design,
// and there is no fourth answer (Ask) to click past.
func TestCheckReadRefusesInAskAndDenyAndPlan(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "secrets.env\n")
	p := New(testCls())
	p.SetIgnoreFile(dir)
	for _, mode := range []Mode{ModeAsk, ModeDeny, ModePlan} {
		p.SetMode(mode)
		v, why := p.CheckRead("secrets.env")
		if v != Deny {
			t.Errorf("mode %d: CheckRead(secrets.env) = %v, want Deny", mode, v)
		}
		if !strings.Contains(why, "secrets.env") || !strings.Contains(why, "allow-reads") {
			t.Errorf("mode %d: refusal %q must name the path and the override", mode, why)
		}
	}
}

// TestCheckReadAllowReadsOverride pins the one documented override: in
// allow-reads the path is allowed and the reason says so, so the journal shows
// an informed allowance rather than a silent pass.
func TestCheckReadAllowReadsOverride(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "secrets.env\n")
	p := New(testCls())
	p.SetIgnoreFile(dir)
	p.SetMode(ModeAllowReads)
	v, why := p.CheckRead("secrets.env")
	if v != Allow {
		t.Fatalf("allow-reads must read ignored paths, got %v", v)
	}
	if !strings.Contains(why, "allow-reads") || !strings.Contains(why, "secrets.env") {
		t.Errorf("allowance reason %q must record why it was granted", why)
	}
}

// TestCheckReadNestedAndAnchored checks the two pattern shapes the tools layer
// actually produces: a nested path matched by a bare basename pattern, and a
// rooted pattern that must not leak to other depths.
func TestCheckReadNestedAndAnchored(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "*.env\n/config.local\n")
	p := New(testCls())
	p.SetIgnoreFile(dir)
	p.SetMode(ModeDeny)
	if v, _ := p.CheckRead("build/secrets.env"); v != Deny {
		t.Errorf("nested basename match: got %v, want Deny", v)
	}
	if v, _ := p.CheckRead("config.local"); v != Deny {
		t.Errorf("rooted match: got %v, want Deny", v)
	}
	if v, _ := p.CheckRead("vendor/config.local"); v != Allow {
		t.Errorf("rooted pattern must not match at depth: got %v, want Allow", v)
	}
	if v, _ := p.CheckRead("main.go"); v != Allow {
		t.Errorf("unmatched path: got %v, want Allow", v)
	}
}

// TestCheckReadNormalizesRel covers the shapes callers hand over: ./ prefixes
// and Windows separators must not turn an excluded path into an allowed one.
// The refusals must come from the pattern, not from a containment error.
func TestCheckReadNormalizesRel(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "secrets.env\n")
	p := New(testCls())
	p.SetIgnoreFile(dir)
	p.SetMode(ModeDeny)
	if v, _ := p.CheckRead("./secrets.env"); v != Deny {
		t.Errorf("./ prefix: got %v, want Deny", v)
	}
	if v, _ := p.CheckRead(`build\secrets.env`); v != Deny {
		t.Errorf("backslash separator: got %v, want Deny", v)
	}
	if v, _ := p.CheckRead("build/../secrets.env"); v != Deny {
		t.Errorf(".. segment: got %v, want Deny", v)
	}
}

// TestCheckReadRefusesDotAndEmpty pins that the rule never gets clever about
// odd inputs: "" and "." are allowed (they mean the root itself), and a ".."
// traversal attempt is outside this rule's job — containment owns that.
func TestCheckReadRefusesDotAndEmpty(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "*\n")
	p := New(testCls())
	p.SetIgnoreFile(dir)
	p.SetMode(ModeDeny)
	for _, rel := range []string{"", ".", "  ", "../outside"} {
		if v, _ := p.CheckRead(rel); v != Allow {
			t.Errorf("CheckRead(%q) = %v, want Allow (not this rule's job)", rel, v)
		}
	}
}

// TestSetIgnoreFileLastCallWins documents reload semantics: SetIgnoreFile
// replaces the rule rather than accumulating patterns, so a reload is honest.
func TestSetIgnoreFileLastCallWins(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "a.txt\n")
	p := New(testCls())
	p.SetIgnoreFile(dir)
	writeIgnoreFile(t, dir, "b.txt\n")
	p.SetIgnoreFile(dir)
	p.SetMode(ModeDeny)
	if v, _ := p.CheckRead("a.txt"); v != Allow {
		t.Errorf("stale pattern still active after reload: got %v", v)
	}
	if v, _ := p.CheckRead("b.txt"); v != Deny {
		t.Errorf("reloaded pattern not active: got %v", v)
	}
}

// TestConcurrentCheckReadWithSetIgnoreFile runs the ladder against a reload
// loop, because SetIgnoreFile takes the same mutex CheckRead does and the race
// detector must find nothing.
func TestConcurrentCheckReadWithSetIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "secrets.env\n")
	p := New(testCls())
	p.SetIgnoreFile(dir)
	p.SetMode(ModeDeny)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			p.SetIgnoreFile(dir)
		}
	}()
	for i := 0; i < 500; i++ {
		p.CheckRead("secrets.env")
		p.CheckRead("main.go")
	}
	<-done
}
