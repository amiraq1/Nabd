// Package ui renders events. Rendering is allowed to change every day;
// the journal it reads from is not.
package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"nabd/internal/display"
	"nabd/internal/event"
	"nabd/internal/presentation"

	"github.com/charmbracelet/lipgloss"
)

// RenderEvent returns the visible form of one event, or "" for events that
// are structure rather than content. Turn boundaries are deliberately
// invisible: they matter to the loop, not to the reader.
func RenderEvent(e event.Event, width int) string {
	if width < 20 {
		width = DefaultWidth
	}
	switch e.Type {
	case event.RunStart:
		return dim.Render("── " + e.Text)

	case event.UserMsg:
		return block("›", e.Text, width, bold)

	case event.ToolStart:
		return block("⚙", callLine(e.Call), width, lipgloss.NewStyle())

	case event.PermAsk:
		line := callLine(e.Call) + " — allow?"
		if reason := presentation.PermissionReasonText(e); reason != "" {
			line += " · " + reason
		}
		return block("?", line, width, warn)

	case event.PermReply:
		st, mark := bad, "✗"
		if e.Decision != event.Deny {
			st, mark = good, "✓"
		}
		line := mark + " " + e.Decision.String()
		if reason := presentation.PermissionReasonText(e); reason != "" {
			line += " · " + reason
		}
		return st.Render(line)

	case event.ToolEnd:
		return toolEnd(e.Call, width)

	case event.Notice:
		return block("⚑", sanitizeEventText(e.Text), width, warn)

	case event.RunError:
		// The failure carries three separate facts: what failed, why each
		// route failed, and what to do next. presentation composes them
		// (and sanitizes them); rendering only lays them out.
		return block("✗", presentation.FormatRunError(e).String(), width, bad)

	case event.Interrupted:
		s := e.Text
		if s == "" {
			s = "stopped"
		}
		return dim.Render("⊘ " + s)

	case event.Compact:
		return block("≡", sanitizeEventText(e.Text), width, dim)

	case event.EventEdit:
		// Summary only: the patch lives in the journal, not on the screen.
		if e.Edit == nil {
			return ""
		}
		s := "✎ " + e.Edit.Path
		if e.Edit.ReadLines > 0 {
			s += fmt.Sprintf(" · read %d lines", e.Edit.ReadLines)
		}
		return dim.Render(s)

	case event.EventEditIntent, event.EventEditAbort:
		// Recovery bookkeeping is journal-only. The committed edit_record
		// remains the single human-facing mutation summary.
		return ""

	case event.EventRead:
		// Summary only: the truncation tail is in the tool_result the model
		// already saw; the screen just marks the fact.
		if e.Read == nil {
			return ""
		}
		if e.Read.Truncated {
			return warn.Render("✂ " + e.Read.Path + " · partially read")
		}
		return ""
	case event.EventRateLimit:
		return warn.Render(fmt.Sprintf("⚑ rate limit %d · retry in %.1fs (attempt %d)", e.Code, e.WaitSec, e.Attempt))

	case event.RunEnd:
		return dim.Render("── " + e.Text)

	case event.EventProviderRoute:
		text, ok := presentation.FormatRouteNotice(e.Route)
		if !ok {
			return ""
		}
		return block("⚑", text, width, warn)

	case event.TextDelta:
		// Streaming text is accumulated into the caller's buffer (chat.go,
		// replay.go, headless.go) and printed once by flushJoin when the
		// buffer is flushed. Rendering it here would duplicate every token.
		return ""

	case event.EventProviderUsage:
		// Accounting/measurement, not screen content. Matches EventCalib.
		return ""

	case event.EventSkillBody:
		// The body is untrusted content and block() performs no ANSI
		// sanitisation. Printing it raw is a terminal-injection path.
		// If it is ever shown, it must go through presentation first.
		return ""

	case event.EventSkills:
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

	case event.Rewind:
		// A rewind is a branch point in the session tree. Hiding it makes
		// replay show an unexplained jump. Reuses the RunStart separator.
		if e.Parent > 0 {
			return dim.Render(fmt.Sprintf("── rewind to #%d", e.Parent))
		}
		return dim.Render("── rewind")

	case event.TurnStart, event.TurnEnd, event.EventCalib:
		// TurnEnd and calibration are structure, not content: nothing to show.
		return ""
	}
	// Unknown type: show it rather than hide it. A newer nabd wrote this.
	return dim.Render("· " + string(e.Type))
}

func callLine(c *event.ToolCall) string {
	if c == nil {
		return "?"
	}
	// Tool names and arguments are model-controlled: sanitize at the render
	// boundary. Same policy as the feed tool summary (single line, no
	// newlines/tabs, redaction enabled). Both ToolStart and PermAsk render
	// through here; the permission prompt is the security-critical path.
	name := display.SanitizeForDisplay(c.Name, display.DisplayPolicy{Redact: true})
	if a := argSummary(c); a != "" {
		return name + " " + display.SanitizeForDisplay(a, display.DisplayPolicy{Redact: true})
	}
	return name
}

// argSummary shows the one argument a human actually wants to see.
func argSummary(c *event.ToolCall) string {
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

func toolEnd(c *event.ToolCall, width int) string {
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
	// The header is built from the model-controlled tool name plus status facts:
	// sanitize the assembled head at the render boundary before wrapping.
	// Single-line policy (no newlines/tabs), redaction enabled.
	head = display.SanitizeForDisplay(head, display.DisplayPolicy{Redact: true})

	lines := wrap(head, width-2)
	var b strings.Builder
	b.WriteString(st.Render(mark + " "))
	b.WriteString(dim.Render(lines[0]))
	for _, l := range lines[1:] {
		b.WriteString("\n  " + dim.Render(l))
	}
	out := b.String()
	if t := tail(sanitizeToolOutput(c.Output)); t != "" {
		out += "\n" + block(" ", t, width, dim)
	}
	return out
}

// sanitizeToolOutput applies the display policy to tool output at the render
// boundary. Same policy as the feed path (truncate_output.go): newlines and
// tabs allowed, credential redaction enabled -- tool output can contain
// secrets (unlike streamed model text). The stored event keeps the raw output.
func sanitizeToolOutput(s string) string {
	return display.SanitizeForDisplay(s, display.DisplayPolicy{AllowNewline: true, AllowTab: true, Redact: true})
}

// sanitizeEventText applies the display policy to untrusted event text (Notice,
// Compact) at the render boundary. Same policy as the feed notice renderer:
// newlines allowed, credential redaction enabled. The stored event keeps the
// raw text.
func sanitizeEventText(s string) string {
	return display.SanitizeForDisplay(s, display.DisplayPolicy{AllowNewline: true, Redact: true})
}

// sanitizeStreamText applies the display policy to streamed model text at
// the render boundary. The accumulation buffer stays raw; only what reaches
// the screen is cleaned. Same policy as feed assistant text: newlines and
// tabs allowed, no credential redaction (model output, not secrets).
func sanitizeStreamText(s string) string {
	return display.SanitizeForDisplay(s, display.DisplayPolicy{AllowNewline: true, AllowTab: true, Redact: false})
}

// flushJoin is the one place text-delta buffers turn into scrollback. Both
// Chat and Replay call it, so a session replays exactly as it was seen:
// the buffered text block first, then the event that ended it, joined into
// a single string because two tea.Println in one Batch race for the
// terminal (P0-1.6). Returns "" when there is nothing to print.
func flushJoin(buf *string, e event.Event, width int) string {
	var prints []string
	if *buf != "" {
		prints = append(prints, renderAgentText(sanitizeStreamText(*buf), width))
		*buf = ""
	}
	if s := RenderEvent(e, width); s != "" {
		prints = append(prints, s)
	}
	return strings.Join(prints, "\n")
}
