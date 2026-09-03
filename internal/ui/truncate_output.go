package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Tool output display limits. Applied at render time only; the original
// output in the journal and FeedItem is never modified.
const (
	maxToolOutputLines  = 100
	maxToolOutputChars  = 4000
	toolOutputHeadLines = 50
	toolOutputTailLines = 50
)

// truncateOutput returns display lines for a tool's output, bounded by the
// limits above. The original string is not modified.
func truncateOutput(output string, width int) []string {
	if output == "" {
		return nil
	}
	// Fast path: small output.
	if utf8.RuneCountInString(output) <= maxToolOutputChars &&
		strings.Count(output, "\n") < maxToolOutputLines {
		return strings.Split(output, "\n")
	}

	lines := strings.Split(output, "\n")
	head := lines
	var tail []string
	if len(lines) > maxToolOutputLines {
		head = lines[:toolOutputHeadLines]
		tail = lines[len(lines)-toolOutputTailLines:]
	}

	var out []string
	for _, l := range head {
		out = append(out, truncateDisplayLine(l, width)...)
	}
	if len(tail) > 0 {
		hidden := len(lines) - toolOutputHeadLines - toolOutputTailLines
		charsHidden := utf8.RuneCountInString(output) - runeCount(head) - runeCount(tail)
		out = append(out, dim.Render(fmt.Sprintf("… %d lines / %d chars hidden …", hidden, max(0, charsHidden))))
		for _, l := range tail {
			out = append(out, truncateDisplayLine(l, width)...)
		}
	}
	// Final char budget.
	return enforceCharBudget(out, maxToolOutputChars)
}

// truncateDisplayLine breaks a single line to fit the viewport width.
func truncateDisplayLine(line string, width int) []string {
	runes := []rune(line)
	if len(runes) <= width {
		return []string{line}
	}
	var out []string
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	out = append(out, string(runes))
	return out
}

// runeCount counts runes across lines.
func runeCount(lines []string) int {
	n := 0
	for _, l := range lines {
		n += utf8.RuneCountInString(l)
	}
	return n
}

// enforceCharBudget trims a slice of lines to stay under max chars.
func enforceCharBudget(lines []string, budget int) []string {
	var total int
	end := 0
	for i, l := range lines {
		total += utf8.RuneCountInString(l)
		if total > budget {
			return lines[:i]
		}
		end = i + 1
	}
	return lines[:end]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
