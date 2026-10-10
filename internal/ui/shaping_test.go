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
