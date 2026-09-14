package ui

import "github.com/charmbracelet/x/ansi"

// Boundary:
// internal/presentation owns candidate ordering (widest to narrowest) and knows
// nothing about terminal width or ansi.StringWidth.
// internal/ui owns the terminal budget and layout constraints.
// Knowledge of display width never crosses this boundary.

// firstFit returns the first variant that fits budget after formatting.
// Variants must be ordered widest-first. format is called lazily, once per
// variant, and stops at the first fit — callers on the refresh path must not
// pay for candidates they never display.
//
// budget is a NET width: deduct any prefix before calling. Never pass a raw
// terminal width.
//
// The caller owns the fallback. The three ladders disagree on it by design:
// footerText truncates (guidance must not vanish), statusLineWithMeta keeps
// its unabridged base (phase and permission text are a red line), and
// runtimeThroughputText blanks out (an optional number is better dropped
// than mangled). modal.go's ladder is a vertical row-count ladder, not a
// width ladder, and is deliberately out of scope.
func firstFit(variants []string, budget int, format func(string) string) (string, bool) {
	if budget <= 0 {
		return "", false
	}
	for _, v := range variants {
		s := v
		if format != nil {
			s = format(v)
		}
		if ansi.StringWidth(s) <= budget {
			return s, true
		}
	}
	return "", false
}
