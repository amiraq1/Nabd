package ui

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// visualRowsSplit splits s into its visual terminal rows as they would
// appear at cell-width w. It mirrors the actual rendering pipeline:
// logical newlines split the string, and each segment is hard-wrapped
// with ansi.Hardwrap exactly like constrainWidth/constrainLines.
//
// This is the single source of truth for row counting so that layout
// reservations and actual rendering can never disagree.
func visualRowsSplit(s string, w int) []string {
	if s == "" {
		return nil
	}
	if w <= 0 {
		w = DefaultWidth
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		wrapped := ansi.Hardwrap(line, w, false)
		if wrapped == "" {
			out = append(out, "")
			continue
		}
		out = append(out, strings.Split(wrapped, "\n")...)
	}
	return out
}

// visualRowsOf counts the visual terminal rows occupied by s when printed
// to a terminal of cell-width w. It delegates to visualRowsSplit so there
// is exactly one definition of row breaking in the package.
func visualRowsOf(s string, w int) int {
	return len(visualRowsSplit(s, w))
}

// constrainWidth hard-wraps s to at most w terminal cells per line.
// ANSI escape sequences are preserved, UTF-8 boundaries are respected.
// If w <= 0 the string is returned unchanged.
func constrainWidth(s string, w int) string {
	if w <= 0 {
		return s
	}
	return ansi.Hardwrap(s, w, false)
}

// constrainLines hard-wraps each logical line of s to w terminal cells.
// Empty lines are preserved. Returns a slice of display lines, each fitting
// within w cells.
func constrainLines(s string, w int) []string {
	if s == "" {
		return []string{""}
	}
	if w <= 0 {
		w = DefaultWidth
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		lw := ansi.StringWidth(line)
		if lw <= w {
			out = append(out, line)
			continue
		}
		out = append(out, strings.Split(ansi.Hardwrap(line, w, false), "\n")...)
	}
	return out
}

// truncateToWidth shortens s to at most w terminal cells, appending tail
// (e.g. "…") if truncation occurred. Uses ansi-aware truncation.
func truncateToWidth(s string, w int, tail string) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, tail)
}

// separatorLine returns a full-width horizontal separator string of exactly
// w terminal cells. Uses ASCII hyphens when NABD_ASCII_ONLY is set;
// otherwise uses the Unicode box-drawing character (─, U+2500).
// The result never wraps.
func separatorLine(w int) string {
	if w <= 0 {
		return ""
	}
	if os.Getenv("NABD_ASCII_ONLY") != "" {
		return asciiSeparatorLine(w)
	}
	// ─ is 1 cell wide (verified by TestSeparatorGlyphWidth)
	return strings.Repeat("─", w)
}

// asciiSeparatorLine returns an ASCII-only separator of exactly w chars.
func asciiSeparatorLine(w int) string {
	if w <= 0 {
		return ""
	}
	return strings.Repeat("-", w)
}

// isValidUTF8 reports whether s is valid UTF-8.
func isValidUTF8(s string) bool {
	return utf8.ValidString(s)
}
