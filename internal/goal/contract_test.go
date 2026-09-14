package goal

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildPreservesArabicObjectiveAndRequiredSections(t *testing.T) {
	objective := "راجع المستودع مراجعة شاملة"
	got, err := Build(objective)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		objective,
		"GOAL:",
		"CONTEXT:",
		"CONSTRAINTS:",
		"DONE WHEN:",
		"VERIFY:",
		"OUTPUT:",
		"STOP RULES:",
		"counting files or searching TODO/FIXME alone is never completion",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("contract missing %q", want)
		}
	}
}

func TestBuildRejectsEmptyOversizedAndControlInput(t *testing.T) {
	if _, err := Build(" \n\t "); !errors.Is(err, ErrEmptyObjective) {
		t.Fatalf("empty error = %v", err)
	}
	if _, err := Build(strings.Repeat("x", MaxObjectiveBytes+1)); !errors.Is(err, ErrObjectiveTooLong) {
		t.Fatalf("oversized error = %v", err)
	}
	if _, err := Build("safe\x00unsafe"); !errors.Is(err, ErrInvalidObjective) {
		t.Fatalf("control error = %v", err)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a, err := Build("fix the failing test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build("  fix the failing test  ")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("equivalent objectives produced different contracts")
	}
}
