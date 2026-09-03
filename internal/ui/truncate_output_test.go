package ui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateOutputSmall verifies small output is unchanged.
func TestTruncateOutputSmall(t *testing.T) {
	out := "line1\nline2\nline3"
	lines := truncateOutput(out, 80)
	got := strings.Join(lines, "\n")
	if got != out {
		t.Errorf("got %q, want %q", got, out)
	}
}

// TestTruncateOutputExceedsLines verifies output > 100 lines is truncated.
func TestTruncateOutputExceedsLines(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	out := sb.String()
	lines := truncateOutput(out, 80)
	// Should have head (50) + tail (50) + marker.
	if len(lines) > maxToolOutputLines+1 {
		t.Errorf("got %d lines, want <= %d", len(lines), maxToolOutputLines+1)
	}
	// First line should be "line 0".
	if lines[0] != "line 0" {
		t.Errorf("first line = %q, want 'line 0'", lines[0])
	}
	// Should contain hidden marker.
	var hasMarker bool
	for _, l := range lines {
		if strings.Contains(l, "hidden") {
			hasMarker = true
			break
		}
	}
	if !hasMarker {
		t.Error("missing hidden content marker")
	}
}

// TestTruncateOutputExceedsChars verifies output > 4000 runes is bounded.
func TestTruncateOutputExceedsChars(t *testing.T) {
	// 5000 runes, single line.
	out := strings.Repeat("x", 5000)
	lines := truncateOutput(out, 80)
	total := 0
	for _, l := range lines {
		total += utf8.RuneCountInString(l)
	}
	if total > maxToolOutputChars {
		t.Errorf("total runes = %d, want <= %d", total, maxToolOutputChars)
	}
}

// TestTruncateOutputArabic verifies Arabic content is preserved (no UTF-8 break).
func TestTruncateOutputArabic(t *testing.T) {
	out := "مرحبا بالعالم\nهذا نص عربي للاختبار"
	lines := truncateOutput(out, 80)
	// All lines should be valid UTF-8.
	for _, l := range lines {
		if !utf8.ValidString(l) {
			t.Errorf("invalid UTF-8 in line: %q", l)
		}
	}
}

// TestTruncateOutputEmoji verifies emoji (multi-byte) survive truncation.
func TestTruncateOutputEmoji(t *testing.T) {
	out := "done ✅\n🎉 party\nnormal line"
	lines := truncateOutput(out, 80)
	for _, l := range lines {
		if !utf8.ValidString(l) {
			t.Errorf("invalid UTF-8 in line: %q", l)
		}
	}
}

// TestTruncateOutputHugeSingleLine verifies a single huge line is broken.
func TestTruncateOutputHugeSingleLine(t *testing.T) {
	out := strings.Repeat("a", 10000)
	lines := truncateOutput(out, 40)
	// Each line should be <= width runes.
	for _, l := range lines {
		if utf8.RuneCountInString(l) > 40 {
			t.Errorf("line too long: %d runes", utf8.RuneCountInString(l))
		}
	}
}

// TestTruncateOutputDoesNotModifyOriginal verifies the original is untouched.
func TestTruncateOutputDoesNotModifyOriginal(t *testing.T) {
	out := "line1\nline2\nline3"
	original := out
	_ = truncateOutput(out, 80)
	if out != original {
		t.Error("original output was modified")
	}
}
