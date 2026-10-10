package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// codeBlockStyle renders fenced code with a subtle background to
// distinguish it from prose. Uses ANSI 236 (dark gray) for the
// background, falling back gracefully on limited terminals.
var codeBlockStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("236")).
	Foreground(lipgloss.Color("252"))

// blockLTR renders a block without RTL reordering, for code and other
// LTR-owned content. Uses legacyWrap directly, bypassing wrapLogicalText.
func blockLTR(sym, s string, width int, st lipgloss.Style) string {
	lines := legacyWrap(s, width-2)
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i == 0 {
			b.WriteString(sym + " ")
		} else {
			b.WriteString("  ")
		}
		b.WriteString(l)
	}
	return st.Render(b.String())
}

// renderAgentText renders agent response text with fenced code blocks
// highlighted. Text outside fences uses the agentText style (with RTL);
// text inside ``` fences uses codeBlockStyle and stays LTR (code is
// direction-neutral; reordering it would corrupt identifiers).
// Unclosed fences render as code to the end.
func renderAgentText(s string, width int) string {
	segments := splitCodeBlocks(s)
	var b strings.Builder
	for i, seg := range segments {
		if i > 0 {
			b.WriteString("\n")
		}
		if seg.isCode {
			// Strip the optional language tag from the opening fence line.
			lines := strings.Split(seg.text, "\n")
			if len(lines) > 0 {
				lines = lines[1:]
			}
			code := strings.TrimSuffix(strings.Join(lines, "\n"), "\n")
			b.WriteString(blockLTR(" ", code, width, codeBlockStyle))
		} else {
			b.WriteString(block(" ", strings.Trim(seg.text, "\n"), width, agentText))
		}
	}
	return b.String()
}

type textSegment struct {
	text   string
	isCode bool
}

var fencePattern = regexp.MustCompile(`(?m)^[ ]{0,3}` + "```")

// splitCodeBlocks splits s on line-anchored ``` fences. Even-indexed segments
// are prose, odd-indexed are code.
func splitCodeBlocks(s string) []textSegment {
	parts := fencePattern.Split(s, -1)
	segs := make([]textSegment, 0, len(parts))
	for i, p := range parts {
		// Skip empty prose segments at boundaries to avoid blank blocks,
		// but keep empty code segments (empty code block is still a block).
		if i%2 == 0 && strings.TrimSpace(p) == "" && len(parts) > 1 {
			continue
		}
		segs = append(segs, textSegment{text: p, isCode: i%2 == 1})
	}
	if len(segs) == 0 {
		return []textSegment{{text: s}}
	}
	return segs
}
