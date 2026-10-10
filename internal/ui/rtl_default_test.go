package ui

import (
	"testing"

	"nabd/internal/rtl"
)

// TestParseRTLModeTTY verifies the TTY-aware default: Reorder on TTY,
// Logical when piped. This is the core of the Arabic-first UX: interactive
// terminals get visual order, pipes keep searchable logical order.
func TestParseRTLModeTTY(t *testing.T) {
	cases := []struct {
		val   string
		isTTY bool
		want  rtl.Mode
	}{
		{"", true, rtl.Reorder},         // unset on TTY -> Reorder
		{"", false, rtl.Logical},        // unset when piped -> Logical
		{"auto", true, rtl.Reorder},     // auto on TTY -> Reorder
		{"auto", false, rtl.Logical},    // auto when piped -> Logical
		{"reorder", false, rtl.Reorder}, // explicit always wins
		{"off", true, rtl.Logical},      // explicit off always wins
		{"unknown", true, rtl.Logical},  // unknown never reorders by accident
	}
	for _, tc := range cases {
		if got := parseRTLMode(tc.val, tc.isTTY); got != tc.want {
			t.Errorf("parseRTLMode(%q, %v) = %v, want %v", tc.val, tc.isTTY, got, tc.want)
		}
	}
}

// TestReadRTLModeRespectsEnv verifies readRTLMode (used by tests)
// picks up NABD_RTL changes without caching.
func TestReadRTLModeRespectsEnv(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	resetRTLModeCache()
	if got := readRTLMode(); got != rtl.Logical {
		t.Fatalf("readRTLMode with NABD_RTL=off = %v, want Logical", got)
	}
	t.Setenv("NABD_RTL", "reorder")
	resetRTLModeCache()
	if got := readRTLMode(); got != rtl.Reorder {
		t.Fatalf("readRTLMode with NABD_RTL=reorder = %v, want Reorder", got)
	}
}
