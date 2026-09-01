// Package ui renders events. Rendering is allowed to change every day;
// the journal it reads from is not.
package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"nabd/internal/agent"

	"github.com/charmbracelet/lipgloss"
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
		return block("?", callLine(e.Call)+" — يسمح؟", width, warn)

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
			s = "توقّف"
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
			s += fmt.Sprintf(" · قرأ %d سطرًا", e.Edit.ReadLines)
		}
		return dim.Render(s)

	case agent.EventRead:
		// Summary only: the truncation tail is in the tool_result the model
		// already saw; the screen just marks the fact.
		if e.Read == nil {
			return ""
		}
		if e.Read.Truncated {
			return warn.Render("✂ " + e.Read.Path + " · مقروء جزئيًا")
		}
		return ""

	case agent.TurnStart, agent.TurnEnd:
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

// tail keeps the last line of output: the verdict is at the bottom.
func tail(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
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

// wrap breaks on spaces at width runes, counting runes not bytes: Arabic
// text is where a byte-based wrapper shows its ignorance.
func wrap(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line, n := "", 0
		for _, w := range strings.Fields(para) {
			r := []rune(w)
			for len(r) > width { // a word longer than the screen
				if n > 0 {
					out = append(out, line)
					line, n = "", 0
				}
				out = append(out, string(r[:width]))
				r = r[width:]
			}
			if len(r) == 0 {
				continue
			}
			if n > 0 && n+1+len(r) > width {
				out = append(out, line)
				line, n = "", 0
			}
			if n > 0 {
				line += " "
				n++
			}
			line += string(r)
			n += len(r)
		}
		out = append(out, line)
	}
	return out
}
