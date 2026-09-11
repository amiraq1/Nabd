package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// CommandHelp renders the slash registry without duplicating command text.
// It is width-bounded and readable on phone terminals.
func CommandHelp(width int) string {
	if width < 20 {
		width = 20
	}
	var lines []string
	for _, cmd := range AllSlashCommands() {
		usage := cmd.Usage
		if width < 40 {
			lines = append(lines, truncateToWidth(usage, width, "…"))
			for _, line := range wrap(cmd.Description, max(1, width-2)) {
				lines = append(lines, "  "+truncateToWidth(line, width-2, "…"))
			}
			continue
		}
		left := 13
		if left > width/2 {
			left = width / 2
		}
		line := fmt.Sprintf("%-*s %s", left, usage, cmd.Description)
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}
	return strings.Join(lines, "\n")
}
