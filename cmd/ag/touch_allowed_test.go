package main

import "testing"

func TestTouchAllowed(t *testing.T) {
	tests := []struct {
		name          string
		termuxVersion string
		force         string
		want          bool
	}{
		{"not termux", "", "", true},
		{"termux without override", "0.112.0", "", false},
		{"termux with force", "0.112.0", "1", true},
		{"termux with empty force", "0.112.0", "", false},
		{"non-termux with force", "", "1", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := touchAllowed(tc.termuxVersion, tc.force)
			if got != tc.want {
				t.Errorf("touchAllowed(%q, %q) = %v, want %v",
					tc.termuxVersion, tc.force, got, tc.want)
			}
		})
	}
}
