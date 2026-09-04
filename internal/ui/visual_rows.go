package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// visualRowsOf counts the visual terminal rows occupied by s when printed
// to a terminal of cell-width w. It accounts for double-width characters
// (CJK, emoji), ANSI escape sequences (width 0), and auto-wrapping.
//
// Rules:
//   - An empty string occupies 0 rows (nothing printed).
//   - A line whose visual width fits within w occupies 1 row.
//   - A line that exceeds w wraps and occupies ceil(lineWidth/w) rows.
//   - w <= 0: falls back to DefaultWidth to avoid divide-by-zero.

// constrainWidth hard-wraps s to at most w terminal cells per line.
// ANSI escape sequences are preserved, UTF-8 boundaries are respected.
// If w <= 0 the string is returned unchanged.

// constrainLines hard-wraps each logical line of s to w terminal cells.
// Empty lines are preserved. Returns a slice of display lines, each fitting
// within w cells.

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
// w terminal cells. Uses the Unicode box-drawing character (─, U+2500) when
// it can be confirmed safe; otherwise ASCII hyphens. The result never wraps.
func separatorLine(w int) string {
	if w <= 0 {
		return ""
	}
	// ─ is 1 cell wide (verified by AllowedUISymbols)
	return strings.Repeat("─", w)
}

// asciiSeparatorLine returns an ASCII-only separator of exactly w chars.

// isValidUTF8 reports whether s is valid UTF-8.
