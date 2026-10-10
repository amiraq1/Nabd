package ui

import (
	"testing"

	"nabd/internal/rtl"
)

func TestParseRTLMode(t *testing.T) {
	cases := []struct {
		name  string
		val   string
		isTTY bool
		want  rtl.Mode
	}{
		// default / auto depends on TTY
		{"unset on tty", "", true, rtl.Reorder},
		{"unset piped", "", false, rtl.Logical},
		{"auto on tty", "auto", true, rtl.Reorder},
		{"auto piped", "auto", false, rtl.Logical},

		// explicit values ignore TTY
		{"reorder piped", "reorder", false, rtl.Reorder},
		{"on", "on", false, rtl.Reorder},
		{"true", "true", false, rtl.Reorder},
		{"mirror", "mirror", true, rtl.ReorderAndMirror},
		{"reorder-and-mirror", "reorder-and-mirror", false, rtl.ReorderAndMirror},

		// opt-out values
		{"off", "off", true, rtl.Logical},
		{"logical", "logical", true, rtl.Logical},
		{"no", "no", true, rtl.Logical},
		{"disable", "disable", true, rtl.Logical},
		{"zero", "0", true, rtl.Logical},
		{"false", "false", true, rtl.Logical},
		{"none", "none", true, rtl.Logical},

		// normalization
		{"upper and spaces", "  OFF ", true, rtl.Logical},
		{"mixed case mirror", " Mirror", true, rtl.ReorderAndMirror},

		// unknown values must never enable reordering
		{"typo", "of", true, rtl.Logical},
		{"garbage", "xyz", true, rtl.Logical},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseRTLMode(c.val, c.isTTY); got != c.want {
				t.Fatalf("parseRTLMode(%q, tty=%v) = %v, want %v",
					c.val, c.isTTY, got, c.want)
			}
		})
	}
}

// Env wiring: only values whose result doesn't depend on the TTY.
func TestReadRTLModeFromEnv(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	if got := readRTLMode(); got != rtl.Logical {
		t.Fatalf("NABD_RTL=off -> %v, want Logical", got)
	}
	t.Setenv("NABD_RTL", "reorder")
	if got := readRTLMode(); got != rtl.Reorder {
		t.Fatalf("NABD_RTL=reorder -> %v, want Reorder", got)
	}
}
