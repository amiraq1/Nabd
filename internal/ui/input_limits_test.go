package ui

import (
	"strings"
	"testing"
)

// runes returns a string of exactly n Arabic 'م' runes (2 bytes each in
// UTF-8), so tests prove rune counting, not byte counting.
func arabicRunes(n int) string {
	return strings.Repeat("م", n)
}

func TestCountInputLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"single line", "abc", 1},
		{"two lines", "a\nb", 2},
		{"leading newline", "\na", 2},
		{"trailing newline starts a line", "a\n", 2},
		{"only newlines", "\n\n\n", 4},
		{"arabic multi-line", "مرحبا\nكيف حالك", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countInputLines(tt.in); got != tt.want {
				t.Errorf("countInputLines(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestInputTooLong(t *testing.T) {
	// Boundary: exactly maxInputRunes is accepted.
	atCap := arabicRunes(maxInputRunes)
	if inputTooLong(atCap) {
		t.Error("input of exactly maxInputRunes must be accepted")
	}
	// Over by one is rejected.
	if !inputTooLong(arabicRunes(maxInputRunes + 1)) {
		t.Error("input of maxInputRunes+1 must be rejected")
	}
	// Emoji count as runes too (4 bytes each in UTF-8).
	if inputTooLong(strings.Repeat("😀", maxInputRunes)) {
		t.Error("emoji string of exactly maxInputRunes must be accepted")
	}
	if !inputTooLong(strings.Repeat("😀", maxInputRunes+1)) {
		t.Error("emoji string of maxInputRunes+1 must be rejected")
	}
	// Line boundary: a 200-line input = 199 newline separators.
	// 200 lines exactly is accepted.
	lines200 := strings.Repeat("a\n", maxInputLines-1) + "a"
	if inputTooLong(lines200) {
		t.Error("input with exactly maxInputLines lines must be accepted")
	}
	// 201 lines (200 newlines) is rejected.
	lines201 := strings.Repeat("a\n", maxInputLines) + "a"
	if !inputTooLong(lines201) {
		t.Error("input with maxInputLines+1 lines must be rejected")
	}
}
