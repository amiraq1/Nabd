package ui

import (
	"strings"
	"testing"
)

func TestShapeArabicTextBasic(t *testing.T) {
	// "مرحبا" should become presentation forms, not logical chars
	got := shapeArabicText("مرحبا")
	// Check that output contains presentation forms (U+FE80-U+FEF4)
	// and NOT the logical forms
	hasPres := false
	hasLogical := false
	for _, r := range got {
		if r >= 0xFE80 && r <= 0xFEF4 {
			hasPres = true
		}
		if r >= 0x0627 && r <= 0x064A {
			hasLogical = true
		}
	}
	if !hasPres {
		t.Errorf("expected presentation forms, got %q", got)
	}
	if hasLogical {
		t.Errorf("logical forms should be shaped, got %q", got)
	}
}

func TestShapedThroughFormatMarkdown(t *testing.T) {
	// Shape first, then format (BiDi)
	shaped := shapeArabicText("مرحبا")
	lines := formatMarkdown(shaped, 50)
	joined := strings.Join(lines, "\n")
	// Should still contain presentation forms after BiDi
	hasPres := false
	for _, r := range joined {
		if r >= 0xFE80 && r <= 0xFEF4 {
			hasPres = true
			break
		}
	}
	if !hasPres {
		t.Errorf("presentation forms lost through formatMarkdown, got %q", joined)
	}
	// Should NOT contain logical Arabic (they were shaped)
	for _, r := range joined {
		if r >= 0x0627 && r <= 0x064A {
			t.Errorf("logical Arabic leaked through, got %q", joined)
			break
		}
	}
}

// TestShapeArabicIdempotent verifies the gate requirement: shaping already-
// shaped text (presentation forms) must not change it further. This prevents
// double-shaping if the input was already processed.
func TestShapeArabicIdempotent(t *testing.T) {
	once := shapeArabicText("مرحبا")
	twice := shapeArabicText(once)
	if once != twice {
		t.Errorf("shaping not idempotent:\n once: %q\ntwice: %q", once, twice)
	}
}

// TestShapeArabicJoinSemantics verifies specific joining behavior:
// بب -> U+FE91 (initial) U+FE90 (final), لا -> single U+FEFB ligature.
func TestShapeArabicJoinSemantics(t *testing.T) {
	// بب: first beh initial, second beh final
	got := shapeArabicText("بب")
	want := "\uFE91\uFE90"
	if got != want {
		t.Errorf("بب -> %q, want %q", got, want)
	}

	// لا: lam-alef ligature (single codepoint)
	got = shapeArabicText("لا")
	want = "\uFEFB"
	if got != want {
		t.Errorf("لا -> %q, want %q", got, want)
	}
}

// TestShapeArabicPassthrough verifies non-Arabic text is unchanged.
func TestShapeArabicPassthrough(t *testing.T) {
	cases := []string{
		"",
		"hello",
		"café",
		"e\u0301", // e + combining acute
		"👨‍👩‍👧‍👦", // emoji ZWJ family
		"123",
		"```code```",
	}
	for _, tc := range cases {
		if got := shapeArabicText(tc); got != tc {
			t.Errorf("passthrough failed for %q: got %q", tc, got)
		}
	}
}
