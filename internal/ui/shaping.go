package ui

import (
	"strings"

	"nabd/internal/rtl"
)

// shapeArabicText converts logical Arabic text to presentation forms using
// the rtl shaping engine. Non-Arabic text passes through unchanged.
// This is display-only: the logical source remains canonical for journal,
// search, and replay. Termux does not shape logical letters (see
// docs/termux-shaping-measurements.md), so explicit shaping is required
// for connected rendering.
func shapeArabicText(s string) string {
	if s == "" {
		return s
	}
	clusters := rtl.ShapeArabic(s)
	if len(clusters) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, c := range clusters {
		b.WriteString(string(c.Visual))
	}
	return b.String()
}
