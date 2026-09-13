package ui

import "strings"

// stripCardGutter removes only the two exact prefixes emitted by the feed:
// the selected-card marker "> " and the unselected-card marker "  ".
// A single leading space is card content and must remain untouched.
func stripCardGutter(s string) string {
	switch {
	case strings.HasPrefix(s, "> "):
		return s[2:]
	case strings.HasPrefix(s, "  "):
		return s[2:]
	default:
		return s
	}
}
