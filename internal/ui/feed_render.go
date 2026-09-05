package ui

import (
	"fmt"
	"strings"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

// renderItems converts feed items to display lines, each constrained to width.
func renderItems(items []presentation.FeedItem, width int) []string {
	var lines []string
	for _, it := range items {
		raw := renderItem(it, width)
		for _, l := range raw {
			// Guarantee every stored line fits within width terminal cells.
			if width > 0 && ansi.StringWidth(l) > width {
				lines = append(lines, strings.Split(ansi.Hardwrap(l, width, false), "\n")...)
			} else {
				lines = append(lines, l)
			}
		}
	}
	return lines
}

// renderItem renders one feed item to one or more lines.
func renderItem(it presentation.FeedItem, width int) []string {
	switch it.Type {
	case presentation.ItemUserMsg:
		return renderUserMsg(it, width)
	case presentation.ItemAssistant:
		return renderAssistant(it, width)
	case presentation.ItemTool:
		return renderTool(it, width)
	case presentation.ItemPermission:
		return renderPerm(it, width)
	case presentation.ItemNotice:
		return renderNotice(it, width)
	case presentation.ItemError:
		return renderError(it, width)
	case presentation.ItemRunBoundary:
		return renderRunBoundary(it, width)
	default:
		return []string{dim.Render(truncateToWidth(fmt.Sprintf("· unknown item %s", it.Type), width, "…"))}
	}
}

func renderUserMsg(it presentation.FeedItem, width int) []string {
	clean := SanitizeForDisplay(it.Text, DisplayPolicy{AllowNewline: true, AllowTab: true, Redact: false})
	var out []string
	for _, line := range wrap("› "+clean, width) {
		out = append(out, bold.Render(line))
	}
	return out
}

func renderAssistant(it presentation.FeedItem, width int) []string {
	var out []string
	text := it.Text
	if text == "" {
		text = "·"
	}
	clean := SanitizeForDisplay(text, DisplayPolicy{AllowNewline: true, AllowTab: true, Redact: false})
	out = append(out, wrap(clean, width)...)
	return out
}

func renderTool(it presentation.FeedItem, width int) []string {
	if it.Tool == nil {
		return nil
	}
	t := it.Tool
	var out []string

	// Status symbol + name.
	sym := toolStatusSymbol(t.Status)
	cleanName := SanitizeForDisplay(t.Name, DisplayPolicy{AllowNewline: false, Redact: true})
	head := fmt.Sprintf("%s %s", sym, cleanName)
	if t.Args != "" {
		cleanArgs := SanitizeForDisplay(t.Args, DisplayPolicy{AllowNewline: false, Redact: true})
		head += " " + truncate(cleanArgs, width/3)
	}
	out = append(out, truncateToWidth(head, width, "…"))

	// Duration / exit info.
	if t.Status == presentation.ToolRunning {
		out = append(out, dim.Render("  ···"))
	} else if t.Duration > 0 {
		out = append(out, dim.Render(truncateToWidth(fmt.Sprintf("  %dms", t.Duration), width, "…")))
	} else if t.ExitCode != 0 {
		out = append(out, bad.Render(truncateToWidth(fmt.Sprintf("  exit %d", t.ExitCode), width, "…")))
	} else if t.Signal != "" {
		out = append(out, bad.Render(truncateToWidth("  · "+t.Signal, width, "…")))
	}

	// Output (truncated and sanitized).
	if t.Output != "" {
		out = append(out, truncateOutput(t.Output, width)...)
	}
	return out
}

func renderPerm(it presentation.FeedItem, width int) []string {
	if it.Perm == nil {
		return nil
	}
	p := it.Perm
	var out []string
	sym := "?"
	switch p.Status {
	case presentation.PermAllow:
		sym = good.Render("✓")
	case presentation.PermDeny:
		sym = bad.Render("✗")
	default:
		sym = warn.Render("?")
	}
	cleanName := SanitizeForDisplay(p.Name, DisplayPolicy{AllowNewline: false, Redact: true})
	head := fmt.Sprintf("%s %s", sym, cleanName)
	if p.Args != "" {
		cleanArgs := SanitizeForDisplay(p.Args, DisplayPolicy{AllowNewline: false, Redact: true})
		head += " " + truncate(cleanArgs, width/3)
	}
	out = append(out, truncateToWidth(head, width, "…"))
	if p.Status == presentation.PermAllow && p.Effective != p.Decision {
		notice := fmt.Sprintf("  · requested %s, applied %s", p.Decision, p.Effective)
		out = append(out, dim.Render(truncateToWidth(notice, width, "…")))
	}
	return out
}

func renderNotice(it presentation.FeedItem, width int) []string {
	clean := SanitizeForDisplay(it.Text, DisplayPolicy{AllowNewline: false, Redact: true})
	return []string{warn.Render(truncateToWidth("⚑ "+clean, width, "…"))}
}

func renderError(it presentation.FeedItem, width int) []string {
	text := it.Text
	if text == "" {
		text = "error"
	}
	clean := SanitizeForDisplay(text, DisplayPolicy{AllowNewline: false, Redact: true})
	return []string{bad.Render(truncateToWidth("✗ "+clean, width, "…"))}
}

func renderRunBoundary(it presentation.FeedItem, width int) []string {
	clean := SanitizeForDisplay(it.Text, DisplayPolicy{AllowNewline: false, Redact: false})
	return []string{dim.Render(truncateToWidth("── "+clean, width, "…"))}
}

// toolStatusSymbol returns a status glyph for a tool card.
func toolStatusSymbol(s presentation.ToolStatus) string {
	switch s {
	case presentation.ToolPending:
		return dim.Render("o")
	case presentation.ToolRunning:
		return warn.Render("~")
	case presentation.ToolDone:
		return good.Render("+")
	case presentation.ToolFailed:
		return bad.Render("x")
	case presentation.ToolDenied:
		return bad.Render("X")
	case presentation.ToolCancelled:
		return dim.Render("X")
	default:
		return "."
	}
}

// truncate shortens a string using ansi-aware visual width.
// Prefer truncateToWidth (visual cells); this is kept for callers using rune count.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	return truncateToWidth(s, max, "…")
}
