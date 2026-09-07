package ui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// formatMarkdown applies minimal Markdown presentation to sanitized text.
func formatMarkdown(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	var out []string
	lines := strings.Split(text, "\n")
	inFenced := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Fenced code blocks
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFenced = !inFenced
			out = append(out, wrap(line, width)...)
			continue
		}

		if inFenced {
			out = append(out, wrap(line, width)...)
			continue
		}

		// Headings
		if _, content, ok := parseHeading(line); ok {
			formatted := bold.Render(formatInline(content))
			// wrapped heading? Or just let wrap handle it.
			out = append(out, wrap(formatted, width)...)
			continue
		}

		// Lists
		if lPrefix, lIndent, content, ok := parseList(line); ok {
			formatted := formatInline(content)
			contentWidth := width - ansi.StringWidth(lIndent)
			if contentWidth < 8 {
				contentWidth = 8
			}
			wrapped := wrap(formatted, contentWidth)
			if len(wrapped) == 0 {
				out = append(out, lPrefix)
			} else {
				out = append(out, lPrefix+wrapped[0])
				for j := 1; j < len(wrapped); j++ {
					out = append(out, lIndent+wrapped[j])
				}
			}
			continue
		}

		// Normal paragraph line
		out = append(out, wrap(formatInline(line), width)...)
	}
	return out
}

func parseHeading(line string) (prefix, content string, ok bool) {
	// Support #, ##, ### followed by a space
	for _, p := range []string{"### ", "## ", "# "} {
		if strings.HasPrefix(line, p) {
			return p, strings.TrimPrefix(line, p), true
		}
	}
	return "", line, false
}

func parseList(line string) (prefix, indent, content string, ok bool) {
	// unordered: ^\s*[-*]\s+
	// ordered: ^\s*\d+\.\s+
	s := line
	spaces := 0
	for _, r := range s {
		if r == ' ' {
			spaces++
		} else {
			break
		}
	}
	trimmed := s[spaces:]

	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		prefix = s[:spaces+2]
		return prefix, strings.Repeat(" ", ansi.StringWidth(prefix)), s[spaces+2:], true
	}

	// check ordered
	digits := 0
	for _, r := range trimmed {
		if unicode.IsDigit(r) {
			digits++
		} else {
			break
		}
	}
	if digits > 0 && len(trimmed) > digits && trimmed[digits] == '.' && len(trimmed) > digits+1 && trimmed[digits+1] == ' ' {
		prefix = s[:spaces+digits+2]
		return prefix, strings.Repeat(" ", ansi.StringWidth(prefix)), s[spaces+digits+2:], true
	}

	return "", "", line, false
}

func formatInline(text string) string {
	var sb strings.Builder
	runes := []rune(text)
	inCode := false
	
	// Two-pass approach or state machine for bold to ensure we don't style incomplete bold.
	// Actually, just find pairs of `**`.
	
	// A bold span may become styled when its closing delimiter arrives. 
	// This means if `**foo` is present, it's just `**foo`. If `**foo**`, it's bold(foo).
	
	i := 0
	for i < len(runes) {
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' && !inCode {
			// Find closing **
			closeIdx := -1
			for j := i + 2; j < len(runes); j++ {
				// Don't format if we encounter ` inside. Wait, code blocks take precedence?
				if runes[j] == '`' {
					// markdown normally doesn't care, but let's just find the closest **
				}
				if j+1 < len(runes) && runes[j] == '*' && runes[j+1] == '*' {
					closeIdx = j
					break
				}
			}
			if closeIdx != -1 {
				// Render bold
				inner := string(runes[i+2 : closeIdx])
				// recursive? Markdown allows `**foo `bar` baz**`.
				sb.WriteString(bold.Render(formatInline(inner)))
				i = closeIdx + 2
				continue
			}
		}
		
		if runes[i] == '`' {
			// Find closing `
			closeIdx := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '`' {
					closeIdx = j
					break
				}
			}
			if closeIdx != -1 {
				// Output literal inner code
				sb.WriteString(string(runes[i : closeIdx+1]))
				i = closeIdx + 1
				continue
			} else {
				// Incomplete code span
				sb.WriteRune(runes[i])
				i++
				continue
			}
		}

		sb.WriteRune(runes[i])
		i++
	}
	return sb.String()
}
