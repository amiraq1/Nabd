package ui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// parseSourceLine checks if line is in the read_file numbered format "<line_number>|<content>".
func parseSourceLine(line string) (int, string, bool) {
	idx := strings.IndexByte(line, '|')
	if idx <= 0 {
		return 0, "", false
	}
	num, err := strconv.Atoi(line[:idx])
	if err != nil || num <= 0 {
		return 0, "", false
	}
	return num, line[idx+1:], true
}

// renderReadFileOutput formats read_file output with a fixed-width gutter,
// right-aligned line numbers, and aligned continuation rows.
func renderReadFileOutput(output string, width int) []string {
	if output == "" {
		return nil
	}
	if width <= 0 {
		width = DefaultWidth
	}

	output = SanitizeForDisplay(output, DisplayPolicy{
		AllowNewline: true,
		AllowTab:     true,
		Redact:       true,
	})
	if output == "" {
		return nil
	}

	lines := strings.Split(output, "\n")
	hasSourceLine := false
	for _, l := range lines {
		if _, _, ok := parseSourceLine(l); ok {
			hasSourceLine = true
			break
		}
	}
	if !hasSourceLine {
		return truncateOutput(output, width)
	}

	head := lines
	var tail []string
	if len(lines) > maxToolOutputLines {
		head = lines[:toolOutputHeadLines]
		tail = lines[len(lines)-toolOutputTailLines:]
	}

	maxLineNum := 0
	for _, l := range head {
		if n, _, ok := parseSourceLine(l); ok && n > maxLineNum {
			maxLineNum = n
		}
	}
	for _, l := range tail {
		if n, _, ok := parseSourceLine(l); ok && n > maxLineNum {
			maxLineNum = n
		}
	}

	digits := len(strconv.Itoa(maxLineNum))
	if digits < 1 {
		digits = 1
	}
	gutterWidth := digits + 4 // " " + digits + " | "

	contentWidth := width - gutterWidth
	// Degrade safely when terminal is too narrow for gutter
	if contentWidth < 4 {
		return truncateOutput(output, width)
	}

	formatChunk := func(chunk []string) []string {
		var res []string
		for _, l := range chunk {
			num, content, ok := parseSourceLine(l)
			if !ok {
				// Truncation/pagination metadata or non-source line: wrap as metadata without gutter
				if l == "" {
					res = append(res, "")
				} else {
					res = append(res, truncateDisplayLine(l, width)...)
				}
				continue
			}

			gutter := fmt.Sprintf(" %*d | ", digits, num)
			contGutter := fmt.Sprintf(" %*s | ", digits, "")

			if content == "" {
				// Preserve blank source line
				res = append(res, gutter)
				continue
			}

			// Wrap content: prefer whitespace boundaries, hard-wrap only when necessary
			wrapped := ansi.Hardwrap(ansi.Wordwrap(content, contentWidth, " \t"), contentWidth, false)
			cLines := strings.Split(wrapped, "\n")
			for i, cl := range cLines {
				if i == 0 {
					res = append(res, gutter+cl)
				} else {
					res = append(res, contGutter+cl)
				}
			}
		}
		return res
	}

	var out []string
	out = append(out, formatChunk(head)...)
	if len(tail) > 0 {
		hidden := len(lines) - toolOutputHeadLines - toolOutputTailLines
		charsHidden := utf8.RuneCountInString(output) - runeCount(head) - runeCount(tail)
		out = append(out, dim.Render(fmt.Sprintf("… %d lines / %d chars hidden …", hidden, max(0, charsHidden))))
		out = append(out, formatChunk(tail)...)
	}

	return enforceCharBudget(out, maxToolOutputChars)
}
