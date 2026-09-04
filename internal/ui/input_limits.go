package ui

import "strings"

// Input limits for the multiline composer. Single source of truth.
const (
	// maxInputRunes caps the composer at 8000 Unicode code points (not
	// bytes). Applied atomically: an edit that would cross the cap is
	// rejected whole rather than silently trimmed.
	maxInputRunes = 8000

	// maxInputLines caps the number of logical lines (separated by \n).
	// The same atomic rule applies.
	maxInputLines = 200
)

// limitNotice is the status text shown when an input mutation is rejected
// because it would cross maxInputRunes or maxInputLines. The default is
// ASCII; the CLI overrides it with the Arabic user-facing string via
// SetLimitNotice (Arabic text lives outside internal/ui, which enforces an
// ASCII-symbol whitelist on its string literals).
var limitNotice = "input limit exceeded: 8000 characters or 200 lines."

// SetLimitNotice overrides the limit-exceeded status text. The CLI passes
// the Arabic user-facing message.
func SetLimitNotice(s string) { limitNotice = s }

// countInputLines returns the number of logical lines in s: empty text is
// zero content lines; otherwise 1 + the number of newline runes. A trailing
// newline starts a new logical line and is counted.
func countInputLines(s string) int {
	if s == "" {
		return 0
	}
	return 1 + strings.Count(s, "\n")
}

// inputTooLong reports whether s crosses either composer limit.
func inputTooLong(s string) bool {
	return countInputLines(s) > maxInputLines || len([]rune(s)) > maxInputRunes
}
