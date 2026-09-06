package ui

import "nabd/internal/display"

// DisplayPolicy controls text sanitization behavior at the display boundary.
type DisplayPolicy = display.DisplayPolicy

// SanitizeForDisplay sanitizes untrusted text at the display boundary before
// cell width calculation, line wrapping, or application styling.
// Original journal bytes are never altered.
func SanitizeForDisplay(untrusted string, p DisplayPolicy) string {
	return display.SanitizeForDisplay(untrusted, p)
}
