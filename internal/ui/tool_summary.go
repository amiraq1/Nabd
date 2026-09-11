package ui

import (
	"fmt"
	"strings"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

// renderToolSummary keeps the lifecycle and tool identity at every width, then
// adds a safe subject and structured metadata while space remains.
func renderToolSummary(t *presentation.ToolCard, width int) string {
	if width <= 0 { width = DefaultWidth }
	prefix := "  " + toolStatusSymbol(t.Status) + " " + toolDisplayName(t.Name)
	subject := toolSubject(t)
	meta := toolSummaryMetadata(t)

	parts := []string{prefix}
	if subject != "" { parts = append(parts, subject) }
	if len(meta) > 0 { parts = append(parts, strings.Join(meta, " · ")) }
	if line := strings.Join(parts, "  "); ansi.StringWidth(line) <= width { return line }

	// Failure state outranks secondary subject/duration at narrow widths.
	if important := toolImportantMetadata(t); important != "" {
		line := prefix + " · " + important
		if ansi.StringWidth(line) <= width { return line }
	}
	if subject != "" {
		avail := width - ansi.StringWidth(prefix) - 2
		if avail > 0 { return prefix + "  " + truncateToWidth(subject, avail, "…") }
	}
	return truncateToWidth(prefix, width, "")
}

func toolSubject(t *presentation.ToolCard) string {
	if t == nil { return "" }
	return toolSummaryText(t.Args)
}

func toolSummaryText(value string) string {
	clean := SanitizeForDisplay(value, DisplayPolicy{AllowNewline: false, AllowTab: false, Redact: true})
	clean = strings.Join(strings.Fields(clean), " ")
	return clean
}

func toolSummaryMetadata(t *presentation.ToolCard) []string {
	var out []string
	if important := toolImportantMetadata(t); important != "" { out = append(out, important) }
	if t.Duration > 0 { out = append(out, dur(t.Duration)) }
	if t.Truncated { out = append(out, "truncated") }
	if t.OutputState == presentation.OutputTruncated { out = append(out, "saved output truncated") }
	if t.OutputState == presentation.OutputUnavailable { out = append(out, "output unavailable") }
	if t.NextOffset != nil { out = append(out, fmt.Sprintf("next offset %d", *t.NextOffset)) }
	return out
}

func toolImportantMetadata(t *presentation.ToolCard) string {
	switch t.Status {
	case presentation.ToolPending:
		return "pending"
	case presentation.ToolRunning:
		return "running"
	case presentation.ToolDenied:
		return "denied"
	case presentation.ToolCancelled:
		return "cancelled"
	}
	if t.ExitCode != 0 { return fmt.Sprintf("exit %d", t.ExitCode) }
	if t.Signal != "" { return toolSummaryText(t.Signal) }
	if t.Status == presentation.ToolFailed { return "failed" }
	return ""
}

func toolStatusSymbol(status presentation.ToolStatus) string {
	switch status {
	case presentation.ToolPending:
		return dim.Render("…")
	case presentation.ToolRunning:
		return warn.Render("⚙")
	case presentation.ToolDone:
		return good.Render("✓")
	case presentation.ToolFailed:
		return bad.Render("✗")
	case presentation.ToolDenied:
		return bad.Render("!")
	case presentation.ToolCancelled:
		return dim.Render("⊘")
	default:
		return dim.Render("·")
	}
}

func renderToolMetadata(t *presentation.ToolCard, width int) []string {
	var rows []string
	add := func(label, value string) {
		value = toolSummaryText(value); if value == "" { return }
		rows = append(rows, dim.Render(truncateToWidth("  "+label+": "+value, width, "…")))
	}
	add("arguments", t.Args)
	if t.ExitCode != 0 { add("exit", fmt.Sprint(t.ExitCode)) }
	add("signal", t.Signal)
	add("error", t.Err)
	if t.Truncated { add("read", "truncated at execution") }
	if t.OutputState == presentation.OutputTruncated { add("output", "saved prefix only") }
	if t.OutputState == presentation.OutputUnavailable { add("output", "unavailable") }
	if t.NextOffset != nil { add("next", fmt.Sprintf("offset=%d", *t.NextOffset)) }
	return rows
}
