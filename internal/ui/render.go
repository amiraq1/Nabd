// Package ui renders events. Rendering is allowed to change every day;
// the journal it reads from is not.
package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"nabd/internal/agent"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// DefaultWidth is a phone in portrait, one hand.
const DefaultWidth = 50

var (
	dim  = lipgloss.NewStyle().Faint(true)
	bold = lipgloss.NewStyle().Bold(true)
	good = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	bad  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
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

// RenderEvent returns the visible form of one event, or "" for events that
// are structure rather than content. Turn boundaries are deliberately
// invisible: they matter to the loop, not to the reader.
func RenderEvent(e agent.Event, width int) string {
	if width < 20 {
		width = DefaultWidth
	}
	switch e.Type {
	case agent.RunStart:
		return dim.Render("── " + e.Text)

	case agent.UserMsg:
		return block("›", e.Text, width, bold)

	case agent.ToolStart:
		return block("⚙", callLine(e.Call), width, lipgloss.NewStyle())

	case agent.PermAsk:
		return block("?", callLine(e.Call)+" — allow?", width, warn)

	case agent.PermReply:
		st, mark := bad, "✗"
		if e.Decision != agent.Deny {
			st, mark = good, "✓"
		}
		return st.Render(mark + " " + e.Decision.String())

	case agent.ToolEnd:
		return toolEnd(e.Call, width)

	case agent.Notice:
		return block("⚑", e.Text, width, warn)

	case agent.RunError:
		return block("✗", e.Err, width, bad)

	case agent.Interrupted:
		s := e.Text
		if s == "" {
			s = "stopped"
		}
		return dim.Render("⊘ " + s)

	case agent.Compact:
		return block("≡", e.Text, width, dim)

	case agent.EventEdit:
		// Summary only: the patch lives in the journal, not on the screen.
		if e.Edit == nil {
			return ""
		}
		s := "✎ " + e.Edit.Path
		if e.Edit.ReadLines > 0 {
			s += fmt.Sprintf(" · read %d lines", e.Edit.ReadLines)
		}
		return dim.Render(s)

	case agent.EventRead:
		// Summary only: the truncation tail is in the tool_result the model
		// already saw; the screen just marks the fact.
		if e.Read == nil {
			return ""
		}
		if e.Read.Truncated {
			return warn.Render("✂ " + e.Read.Path + " · partially read")
		}
	case agent.EventRateLimit:
		return warn.Render(fmt.Sprintf("⚑ rate limit %d · retry in %.1fs (attempt %d)", e.Code, e.WaitSec, e.Attempt))

	case agent.RunEnd:
		return dim.Render("── " + e.Text)

	case agent.TurnStart, agent.TurnEnd, agent.EventCalib:
		// TurnEnd and calibration are structure, not content: nothing to show.
		return ""
	}
	// Unknown type: show it rather than hide it. A newer nabd wrote this.
	return dim.Render("· " + string(e.Type))
}

func callLine(c *agent.ToolCall) string {
	if c == nil {
		return "?"
	}
	if a := argSummary(c); a != "" {
		return c.Name + " " + a
	}
	return c.Name
}

// argSummary shows the one argument a human actually wants to see.
func argSummary(c *agent.ToolCall) string {
	if c == nil || len(c.Args) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(c.Args, &m) != nil {
		return ""
	}
	for _, k := range []string{"cmd", "path", "pattern", "query"} {
		if v, ok := m[k]; ok {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func toolEnd(c *agent.ToolCall, width int) string {
	if c == nil {
		return ""
	}
	head := c.Name
	st, mark := bad, "✗"
	if c.OK {
		st, mark = good, "✓"
	} else if c.Exit != 0 {
		head += fmt.Sprintf(" · exit %d", c.Exit)
	} else if c.Signal != "" {
		head += " · " + c.Signal
	}
	if c.MS > 0 {
		head += " · " + dur(c.MS)
	}
	out := st.Render(mark+" ") + dim.Render(head)
	if t := tail(c.Output); t != "" {
		out += "\n" + block(" ", t, width, dim)
	}
	return out
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
	if width < 8 {
		width = 8
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
