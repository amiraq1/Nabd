package rtl

import "testing"

// TestMirrorTableEntryCount pins the generated table to the Unicode 17.0.0
// BidiMirroring.txt entry count. The conformance gate compares every data-file
// entry against Mirror; this test catches a table that was regenerated with a
// different Unicode version.
func TestMirrorTableEntryCount(t *testing.T) {
	if got := mirrorEntryCount(); got != 428 {
		t.Fatalf("mirror table has %d entries, want 428 (Unicode 17.0.0)", got)
	}
}
