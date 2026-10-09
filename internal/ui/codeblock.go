package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// codeBlockStyle renders fenced code with a subtle background to
// distinguish it from prose. Uses ANSI 236 (dark gray) for the
// background, falling back gracefully on limited terminals.
var codeBlockStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("236")).
	Foreground(lipgloss.Color("252")).
	Padding(0, 1)

// renderAgentText renders agent response text with fenced code blocks
// highlighted. Text outside fences uses the agentText style; text inside
// ``` fences uses codeBlockStyle. Unclosed fences render as code to the
// end (fail-visible rather than fail-confusing).
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
			code := strings.Join(lines, "\n")
			b.WriteString(codeBlockStyle.Render(block(" ", code, width, lipgloss.NewStyle())))
		} else {
			b.WriteString(block(" ", seg.text, width, agentText))
		}
	}
	return b.String()
}

type textSegment struct {
	text   string
	isCode bool
}

// splitCodeBlocks splits s on ``` fences. Even-indexed segments are
// prose, odd-indexed are code.
func splitCodeBlocks(s string) []textSegment {
	parts := strings.Split(s, "```")
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
