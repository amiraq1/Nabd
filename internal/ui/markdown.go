package ui

import (
	"strings"
	"unicode"

	"nabd/internal/rtl"

	"github.com/charmbracelet/x/ansi"
)

// formatMarkdown applies minimal Markdown presentation to sanitized text.
func formatMarkdown(text string, width int) []string {
	if rtlDisplayMode() == rtl.Logical {
		return formatMarkdownLogical(text, width)
	}
	return formatMarkdownVisual(text, width)
}

func formatMarkdownVisual(text string, width int) []string {
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
			out = append(out, renderMarkdownSemantic(line, []rtl.Span{{
				Start: 0, End: len(line), Kind: rtl.Code,
			}}, width)...)
			continue
		}

		if inFenced {
			spans := []rtl.Span(nil)
			if line != "" {
				spans = []rtl.Span{{Start: 0, End: len(line), Kind: rtl.Code}}
			}
			out = append(out, renderMarkdownSemantic(line, spans, width)...)
			continue
		}

		// Headings
		if _, content, ok := parseHeading(line); ok {
			logical, spans := parseInlineSemantic(content, styleBold)
			out = append(out, renderMarkdownSemantic(logical, spans, width)...)
			continue
		}

		// Lists
		if lPrefix, lIndent, content, ok := parseList(line); ok {
			logical, spans := parseInlineSemantic(content, styleNone)
			contentWidth := width - ansi.StringWidth(lIndent)
			if contentWidth < 8 {
				contentWidth = 8
			}
			wrapped := renderMarkdownSemantic(logical, spans, contentWidth)
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
		logical, spans := parseInlineSemantic(line, styleNone)
		out = append(out, renderMarkdownSemantic(logical, spans, width)...)
	}
	return out
}

// formatMarkdownLogical preserves the established fast path byte-for-byte
// when RTL visual processing is disabled. This keeps the default mode cheap
// and makes NABD_RTL=logical a true rollback rather than a second renderer.
func formatMarkdownLogical(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	var out []string
	lines := strings.Split(text, "\n")
	inFenced := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFenced = !inFenced
			out = append(out, legacyWrap(line, width)...)
			continue
		}
		if inFenced {
			out = append(out, legacyWrap(line, width)...)
			continue
		}
		if _, content, ok := parseHeading(line); ok {
			out = append(out, legacyWrap(bold.Render(formatInline(content)), width)...)
			continue
		}
		if lPrefix, lIndent, content, ok := parseList(line); ok {
			formatted := formatInline(content)
			contentWidth := width - ansi.StringWidth(lIndent)
			if contentWidth < 8 {
				contentWidth = 8
			}
			wrapped := legacyWrap(formatted, contentWidth)
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
		out = append(out, legacyWrap(formatInline(line), width)...)
	}
	return out
}

func renderMarkdownSemantic(logical string, spans []rtl.Span, width int) []string {
	lines, err := layoutSemanticText(logical, spans, width)
	if err != nil {
		return semanticFallback(logical, width)
	}
	return lines
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

// parseInlineSemantic is the structured inline Markdown adapter. Delimiters
// are recognized while the logical output is built, so byte offsets always
// refer to the exact string sent to rtl.Layout. It deliberately recognizes
// only the same minimal syntax the previous renderer supported.
func parseInlineSemantic(text string, inheritedStyle uint16) (string, []rtl.Span) {
	var sb strings.Builder
	var spans []rtl.Span
	appendChunk := func(s string, kind rtl.SpanKind, style uint16) {
		if s == "" {
			return
		}
		start := sb.Len()
		sb.WriteString(s)
		if kind != rtl.Prose || style != styleNone {
			spans = append(spans, rtl.Span{
				Start: start, End: sb.Len(), Kind: kind, StyleID: style,
			})
		}
	}

	i := 0
	for i < len(text) {
		if strings.HasPrefix(text[i:], "**") {
			if rel := strings.Index(text[i+2:], "**"); rel >= 0 {
				closeAt := i + 2 + rel
				inner, innerSpans := parseInlineSemantic(text[i+2:closeAt], styleBold)
				base := sb.Len()
				sb.WriteString(inner)
				for _, span := range innerSpans {
					span.Start += base
					span.End += base
					spans = append(spans, span)
				}
				i = closeAt + 2
				continue
			}
		}

		if text[i] == '`' {
			if rel := strings.IndexByte(text[i+1:], '`'); rel >= 0 {
				closeAt := i + 1 + rel
				appendChunk(text[i:closeAt+1], rtl.Code, inheritedStyle)
				i = closeAt + 1
				continue
			}
		}

		next := len(text)
		if rel := strings.Index(text[i+1:], "**"); rel >= 0 && i+1+rel < next {
			next = i + 1 + rel
		}
		if rel := strings.IndexByte(text[i+1:], '`'); rel >= 0 && i+1+rel < next {
			next = i + 1 + rel
		}
		if next == i {
			next++
		}
		appendChunk(text[i:next], rtl.Prose, inheritedStyle)
		i = next
	}
	return sb.String(), spans
}

// formatInline is the established logical-mode formatter. The visual RTL
// path uses parseInlineSemantic instead and emits ANSI after layout.
func formatInline(text string) string {
	var sb strings.Builder
	runes := []rune(text)
	inCode := false
	i := 0
	for i < len(runes) {
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' && !inCode {
			closeIdx := -1
			for j := i + 2; j < len(runes); j++ {
				if j+1 < len(runes) && runes[j] == '*' && runes[j+1] == '*' {
					closeIdx = j
					break
				}
			}
			if closeIdx != -1 {
				sb.WriteString(bold.Render(formatInline(string(runes[i+2 : closeIdx]))))
				i = closeIdx + 2
				continue
			}
		}
		if runes[i] == '`' {
			closeIdx := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '`' {
					closeIdx = j
					break
				}
			}
			if closeIdx != -1 {
				sb.WriteString(string(runes[i : closeIdx+1]))
				i = closeIdx + 1
				continue
			}
		}
		sb.WriteRune(runes[i])
		i++
	}
	return sb.String()
}
