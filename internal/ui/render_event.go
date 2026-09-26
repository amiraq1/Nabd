// Package ui renders events. Rendering is allowed to change every day;
// the journal it reads from is not.
package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	"github.com/charmbracelet/lipgloss"
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
		line := callLine(e.Call) + " — allow?"
		if reason := presentation.PermissionReasonText(e); reason != "" {
			line += " · " + reason
		}
		return block("?", line, width, warn)

	case agent.PermReply:
		st, mark := bad, "✗"
		if e.Decision != agent.Deny {
			st, mark = good, "✓"
		}
		line := mark + " " + e.Decision.String()
		if reason := presentation.PermissionReasonText(e); reason != "" {
			line += " · " + reason
		}
		return st.Render(line)

	case agent.ToolEnd:
		return toolEnd(e.Call, width)

	case agent.Notice:
		return block("⚑", e.Text, width, warn)

	case agent.RunError:
		// The failure carries three separate facts: what failed, why each
		// route failed, and what to do next. presentation composes them
		// (and sanitizes them); rendering only lays them out.
		return block("✗", presentation.FormatRunError(e).String(), width, bad)

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

	case agent.EventEditIntent, agent.EventEditAbort:
		// Recovery bookkeeping is journal-only. The committed edit_record
		// remains the single human-facing mutation summary.
		return ""

	case agent.EventRead:
		// Summary only: the truncation tail is in the tool_result the model
		// already saw; the screen just marks the fact.
		if e.Read == nil {
			return ""
		}
		if e.Read.Truncated {
			return warn.Render("✂ " + e.Read.Path + " · partially read")
		}
		return ""
	case agent.EventRateLimit:
		return warn.Render(fmt.Sprintf("⚑ rate limit %d · retry in %.1fs (attempt %d)", e.Code, e.WaitSec, e.Attempt))

	case agent.RunEnd:
		return dim.Render("── " + e.Text)

	case agent.EventProviderRoute:
		text, ok := presentation.FormatRouteNotice(e.Route)
		if !ok {
			return ""
		}
		return block("⚑", text, width, warn)

	case agent.TextDelta:
		// Streaming text is accumulated into the caller's buffer (chat.go,
		// replay.go, headless.go) and printed once by flushJoin when the
		// buffer is flushed. Rendering it here would duplicate every token.
		return ""

	case agent.EventProviderUsage:
		// Accounting/measurement, not screen content. Matches EventCalib.
		return ""

	case agent.EventSkillBody:
		// The body is untrusted content and block() performs no ANSI
		// sanitisation. Printing it raw is a terminal-injection path.
		// If it is ever shown, it must go through presentation first.
		return ""

	case agent.EventSkills:
		// Skill inventory: what the model was told, from which scope.
		// Summary only — names, paths and bodies are not printed.
		// An empty inventory (nil or legacy record without the field) carries
		// no information, same rule as a complete read: do not print.
		if len(e.Skills) == 0 {
			return ""
		}
		proj := 0
		for _, s := range e.Skills {
			if s.Scope == "project" {
				proj++
			}
		}
		return dim.Render(fmt.Sprintf("⚑ %d skills · %d project", len(e.Skills), proj))

	case agent.Rewind:
		// A rewind is a branch point in the session tree. Hiding it makes
		// replay show an unexplained jump. Reuses the RunStart separator.
		if e.Parent > 0 {
			return dim.Render(fmt.Sprintf("── rewind to #%d", e.Parent))
		}
		return dim.Render("── rewind")

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
	// How much of the result nobody saw is part of the verdict, not a
	// footnote: the in-payload marker sits at the bottom of the output and
	// the feed keeps only the last few lines, so the marker can be clipped
	// away by the very truncation it announces.
	if c.TruncatedBytes > 0 {
		head += fmt.Sprintf(" · ✂ %d bytes cut", c.TruncatedBytes)
	}
	// The head is wrapped rather than emitted as one line: it now carries
	// enough facts to exceed a phone terminal, and an overflowing row breaks
	// the layout's width contract.
	lines := wrap(head, width-2)
	var b strings.Builder
	b.WriteString(st.Render(mark + " "))
	b.WriteString(dim.Render(lines[0]))
	for _, l := range lines[1:] {
		b.WriteString("\n  " + dim.Render(l))
	}
	out := b.String()
	if t := tail(c.Output); t != "" {
		out += "\n" + block(" ", t, width, dim)
	}
	return out
}

// flushJoin is the one place text-delta buffers turn into scrollback. Both
// Chat and Replay call it, so a session replays exactly as it was seen:
// the buffered text block first, then the event that ended it, joined into
// a single string because two tea.Println in one Batch race for the
// terminal (P0-1.6). Returns "" when there is nothing to print.
func flushJoin(buf *string, e agent.Event, width int) string {
	var prints []string
	if *buf != "" {
		prints = append(prints, block(" ", *buf, width, lipgloss.NewStyle()))
		*buf = ""
	}
	if s := RenderEvent(e, width); s != "" {
		prints = append(prints, s)
	}
	return strings.Join(prints, "\n")
}
