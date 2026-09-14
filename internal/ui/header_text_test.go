package ui

import (
	"testing"
)

// TestHeaderTextBlankWhenNoBaseAndNoBranch covers the early-return short
// circuit in headerText: when there is neither a base header nor an enabled,
// populated git branch, the function must return "" without consulting
// firstFit. This path had no coverage before.
func TestHeaderTextBlankWhenNoBaseAndNoBranch(t *testing.T) {
	f := NewFeed()
	// Defaults: m.header == "", gitHeaderEnabled == false, gitBranch == "".
	if f.headerText(80) != "" {
		t.Fatalf("expected empty header with no base and no branch, got %q", f.headerText(80))
	}
	// Enabling git but with no resolved branch must still yield "".
	f.SetGitHeader(true)
	if f.headerText(80) != "" {
		t.Fatalf("expected empty header with git enabled but no branch, got %q", f.headerText(80))
	}
}

// TestHeaderTextBaseOnly verifies that a base header alone renders even
// without any git branch, so the early return does not swallow the base.
func TestHeaderTextBaseOnly(t *testing.T) {
	f := NewFeed()
	f.SetHeader("nabd")
	if got := f.headerText(80); got != "nabd" {
		t.Fatalf("expected base-only header %q, got %q", "nabd", got)
	}
}

// TestHeaderTextBranchOnly verifies that a resolved git branch alone renders
// even without a base header, so the early return does not swallow the branch.
func TestHeaderTextBranchOnly(t *testing.T) {
	f := NewFeed()
	f.SetGitHeader(true)
	f.gitBranch, f.gitDirty = "main", 0
	if got := f.headerText(80); got != "main (clean)" {
		t.Fatalf("expected branch-only header %q, got %q", "main (clean)", got)
	}
}
