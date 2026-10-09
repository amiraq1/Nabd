package ui

import (
	"os"
	"testing"

	"nabd/internal/rtl"
)

// TestRTLDefaultIsReorder verifies RTL reordering is ON by default.
// Nabd is an Arabic-first tool; Arabic must display correctly without
// requiring users to discover NABD_RTL. Opt-out via NABD_RTL=off remains.
func TestRTLDefaultIsReorder(t *testing.T) {
	os.Unsetenv("NABD_RTL")
	if got := rtlDisplayMode(); got != rtl.Reorder {
		t.Fatalf("default RTL mode = %v, want %v (Reorder)", got, rtl.Reorder)
	}
}

// TestRTLOptOut verifies NABD_RTL=off still disables reordering.
func TestRTLOptOut(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	if got := rtlDisplayMode(); got != rtl.Logical {
		t.Fatalf("NABD_RTL=off mode = %v, want %v (Logical)", got, rtl.Logical)
	}
}
