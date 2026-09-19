package ui

// render_text.go is the domain-free text layer: measuring, wrapping,
// truncating and styling strings. It knows nothing about sessions, tools
// or providers. The import set is enforced by render_layers_test.go.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// DefaultWidth is a phone in portrait, one hand.
const DefaultWidth = 50

var (
	dim   = lipgloss.NewStyle().Faint(true)
	bold  = lipgloss.NewStyle().Bold(true)
	good  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	bad   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warn  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	green = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

	userMsgBg = lipgloss.CompleteColor{TrueColor: "#303030", ANSI256: "236", ANSI: "8"}
	userMsgFg = lipgloss.CompleteColor{TrueColor: "#E0E0E0", ANSI256: "254", ANSI: "15"}

	userCardStyle = lipgloss.NewStyle().Background(userMsgBg).Foreground(userMsgFg)
	userRoleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true).Background(userMsgBg)
)

// AllowedUISymbols is the strict whitelist of non-ASCII glyphs permitted in UI string literals
// and UI error displays. These are UI boundary, status, and decoration symbols, neither Arabic nor Latin.
var AllowedUISymbols = map[rune]bool{
	'⚙': true, // U+2699 ToolStart icon
	'✓': true, // U+2713 Tool success / PermReply allow
	'✗': true, // U+2717 Tool failure / RunError
	'✂': true, // U+2702 Truncation cut icon
	'⚑': true, // U+2691 Notice icon
	'›': true, // U+203A User prompt prefix
	'─': true, // U+2500 RunStart separator bar
	'⊘': true, // U+2298 Interrupted icon
	'≡': true, // U+2261 Compact icon
	'✎': true, // U+270E Edit record icon
	'·': true, // U+00B7 Middle dot separator
	'…': true, // U+2026 Ellipsis
	'—': true, // U+2014 Em dash
	'▌': true, // U+258C Prompt cursor block
	'→': true, // U+2192 Arrow
}

// maxTailLines is how many trailing lines of a tool result the phone screen
// keeps: enough to read a short glob/read result whole, shallow enough that
// long shell logs still end at the verdict. A one-line tail collapses a 5-row
// glob listing to a single file — that is the "glob * → one line" defect,
// because the result set itself vanishes and only the bottom line survives.
const maxTailLines = 8

// tail keeps the trailing lines of output. The verdict is at the bottom, but a
// single-line tail hides every completed line above it — fatal for list
// producers like glob, where the rows ARE the answer. Showing the last few
// lines preserves the bottom verdict/tail while keeping the listing readable.
func tail(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxTailLines {
		return s
	}
	kept := lines[len(lines)-maxTailLines:]
	kept[0] = "… " + kept[0]
	return strings.Join(kept, "\n")
}

func dur(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// block prefixes the first line with sym and indents the rest by two.
func block(sym, s string, width int, st lipgloss.Style) string {
	lines := wrap(s, width-2)
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

// wrap breaks text into lines of at most width terminal cells, using
// ansi.StringWidth for visual measurement. This correctly handles Arabic,
// Emoji, CJK, combining marks, and ANSI escape sequences.
func wrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" {
		return []string{""}
	}
	wrapped := ansi.Hardwrap(ansi.Wordwrap(s, width, " \t"), width, false)
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// partialTail is the live view of text still streaming: the last n wrapped
// lines, marked with … when older lines were cut. A phone shows progress
// without scrolling the answer past the reader; the scrollback gets the
// whole block at flush time.
func partialTail(buf string, n, width int) string {
	s := strings.TrimSpace(buf)
	if s == "" || n <= 0 {
		return ""
	}
	if width < 20 {
		width = DefaultWidth
	}
	lines := wrap(s, width-2)
	more := len(lines) > n
	if more {
		lines = lines[len(lines)-n:]
	}
	for i := range lines {
		pre := "  "
		if i == 0 && more {
			pre = "… "
		}
		lines[i] = pre + lines[i]
	}
	return strings.Join(lines, "\n")
}
