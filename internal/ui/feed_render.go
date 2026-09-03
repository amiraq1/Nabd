package ui

import (
	"fmt"
	"unicode/utf8"

	"nabd/internal/presentation"
)

// renderItems converts feed items to display lines.
func renderItems(items []presentation.FeedItem, width int) []string {
	var lines []string
	for _, it := range items {
		lines = append(lines, renderItem(it, width)...)
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
		return []string{dim.Render(fmt.Sprintf("· unknown item %s", it.Type))}
	}
}

func renderUserMsg(it presentation.FeedItem, width int) []string {
	var out []string
	for _, line := range wrap("› "+it.Text, width) {
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
	for _, line := range wrap(text, width) {
		out = append(out, line)
	}
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
	head := fmt.Sprintf("%s %s", sym, t.Name)
	if t.Args != "" {
		head += " " + truncate(t.Args, width/3)
	}
	out = append(out, head)

	// Duration / exit info.
	if t.Status == presentation.ToolRunning {
		out = append(out, dim.Render("  ···"))
	} else if t.Duration > 0 {
		out = append(out, dim.Render(fmt.Sprintf("  %dms", t.Duration)))
	} else if t.ExitCode != 0 {
		out = append(out, bad.Render(fmt.Sprintf("  exit %d", t.ExitCode)))
	} else if t.Signal != "" {
		out = append(out, bad.Render("  · "+t.Signal))
	}

	// Output (truncated).
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
	head := fmt.Sprintf("%s %s", sym, p.Name)
	if p.Args != "" {
		head += " " + truncate(p.Args, width/3)
	}
	out = append(out, head)
	if p.Status == presentation.PermAllow && p.Effective != p.Decision {
		out = append(out, dim.Render(fmt.Sprintf("  · requested %s, applied %s", p.Decision, p.Effective)))
	}
	return out
}

func renderNotice(it presentation.FeedItem, width int) []string {
	return []string{warn.Render("⚑ " + it.Text)}
}

func renderError(it presentation.FeedItem, width int) []string {
	text := it.Text
	if text == "" {
		text = "error"
	}
	return []string{bad.Render("✗ " + text)}
}

func renderRunBoundary(it presentation.FeedItem, width int) []string {
	if it.RunBoundary == "end" {
		return []string{dim.Render("── " + it.Text)}
	}
	return []string{dim.Render("── " + it.Text)}
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

// truncate shortens a string to max runes, preserving UTF-8.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// wrap breaks text into lines of at most width runes.
