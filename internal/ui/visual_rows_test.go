package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestVisualRowsOfEmpty(t *testing.T) {
	if got := visualRowsOf("", 20); got != 0 {
		t.Errorf("empty string: got %d rows, want 0", got)
	}
}

func TestVisualRowsOfEmptyLinesPreserved(t *testing.T) {
	// "\n\n" = 3 logical lines (all empty), each stays as one row.
	got := visualRowsSplit("\n\n", 20)
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %q", len(got), got)
	}
	for i, r := range got {
		if r != "" {
			t.Errorf("row %d: got %q, want empty string", i, r)
		}
	}
}

func TestVisualRowsOfTrailingNewline(t *testing.T) {
	// "a\n" = 2 logical lines ("a", "").
	got := visualRowsSplit("a\n", 20)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %q", len(got), got)
	}
	if got[0] != "a" || got[1] != "" {
		t.Errorf("got %q, want [\"a\", \"\"]", got)
	}
}

func TestVisualRowsOfExactWidth(t *testing.T) {
	line := strings.Repeat("x", 20)
	got := visualRowsSplit(line, 20)
	if len(got) != 1 {
		t.Errorf("exact width line: got %d rows, want 1", len(got))
	}
	if got[0] != line {
		t.Errorf("content mismatch: got %q", got[0])
	}
}

func TestVisualRowsOfWrap(t *testing.T) {
	// 30 ASCII chars at width 10 -> 3 rows.
	line := strings.Repeat("x", 30)
	got := visualRowsSplit(line, 10)
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %q", len(got), got)
	}
	for i, r := range got {
		if len(r) != 10 {
			t.Errorf("row %d: got %d width, want 10", i, len(r))
		}
	}
}

func TestVisualRowsOfNonPositiveWidth(t *testing.T) {
	// width <= 0 uses DefaultWidth (50).
	got := visualRowsOf(strings.Repeat("x", 100), 0)
	if got != 2 {
		t.Errorf("width=0 with 100 chars, DefaultWidth=50: got %d rows, want 2", got)
	}
	gotNeg := visualRowsOf(strings.Repeat("x", 100), -5)
	if gotNeg != 2 {
		t.Errorf("width=-5: got %d rows, want 2", gotNeg)
	}
}

// TestSeparatorGlyphWidth measures the separator glyphs instead of assuming
// their width. separatorLine and the slash menu both build a run of one rune
// and rely on it occupying exactly one cell; AllowedUISymbols only permits a
// rune, it says nothing about its terminal width.
func TestSeparatorGlyphWidth(t *testing.T) {
	for _, glyph := range []string{"\u2500", "-"} {
		if got := ansi.StringWidth(glyph); got != 1 {
			t.Fatalf("separator glyph %q: width %d, want 1", glyph, got)
		}
	}
	for _, w := range []int{1, 20, 50, 80} {
		if got := ansi.StringWidth(separatorLine(w)); got != w {
			t.Fatalf("separatorLine(%d): width %d", w, got)
		}
		if got := ansi.StringWidth(asciiSeparatorLine(w)); got != w {
			t.Fatalf("asciiSeparatorLine(%d): width %d", w, got)
		}
	}
}
