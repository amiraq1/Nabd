package toolvocab

import "testing"

// TestClassifications is the fail-closed contract for the binary's tool
// vocabulary: a typo must never silently reclassify a tool (read vs execute)
// or drop a guard obligation. Every compiled-in name is pinned here; unknown
// names must fail closed on all three predicates.
func TestClassifications(t *testing.T) {
	cases := []struct {
		name     string
		readOnly bool
		guarded  bool
	}{
		{"read_file", true, false},
		{"write_file", false, false},
		{"edit_file", false, false},
		{"bash", false, false},
		{"skill", true, true},
		{"glob", true, false},
		{"grep", true, false},
	}
	if len(cases) != len(names) {
		t.Fatalf("table has %d entries for %d vocabulary names; update the table", len(cases), len(names))
	}
	for _, tc := range cases {
		if !Has(tc.name) {
			t.Errorf("Has(%q) = false, want true", tc.name)
		}
		if got := IsReadOnly(tc.name); got != tc.readOnly {
			t.Errorf("IsReadOnly(%q) = %v, want %v", tc.name, got, tc.readOnly)
		}
		if got := Guarded(tc.name); got != tc.guarded {
			t.Errorf("Guarded(%q) = %v, want %v", tc.name, got, tc.guarded)
		}
	}
	// Fail-closed: unknown names are neither read-only nor guarded.
	for _, unknown := range []string{"", "READ_FILE", "read-file", "exec", "python"} {
		if Has(unknown) {
			t.Errorf("Has(%q) = true, want false", unknown)
		}
		if IsReadOnly(unknown) {
			t.Errorf("IsReadOnly(%q) = true, want false (fail-closed)", unknown)
		}
		if Guarded(unknown) {
			t.Errorf("Guarded(%q) = true, want false (fail-closed)", unknown)
		}
	}
}

// TestNamesReturnsCopy ensures callers cannot mutate the package vocabulary.
func TestNamesReturnsCopy(t *testing.T) {
	a := Names()
	if len(a) != len(names) {
		t.Fatalf("Names() has %d entries, want %d", len(a), len(names))
	}
	a[0] = "corrupted"
	if names[0] == "corrupted" || !Has(names[0]) {
		t.Fatal("Names() does not return an independent copy")
	}
}
