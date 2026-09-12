package ui

import (
	"fmt"
	"strings"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

func renderItems(items []presentation.FeedItem, width int, toolsExpanded ...bool) []string {
	lines, _ := renderItemsWithOffsets(items, width, toolsExpanded...)
	return lines
}

func renderItemsWithOffsets(items []presentation.FeedItem, width int, toolsExpanded ...bool) ([]string, []int) {
	return flattenBlocks(renderBlocks(items, width, toolsExpanded...))
}

func renderItem(it presentation.FeedItem, width int, toolsExpanded ...bool) []string {
	isExpanded := len(toolsExpanded) > 0 && toolsExpanded[0]
	switch it.Type {
	case presentation.ItemUserMsg:
		return renderUserMsg(it, width)
	case presentation.ItemAssistant:
		return renderAssistant(it, width)
	case presentation.ItemTool:
		return renderTool(it, width, isExpanded)
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
	if width <= 0 {
		width = DefaultWidth
	}
	padLeft, padRight := 0, 0
	if width >= 4 {
		padLeft, padRight = 1, 1
	} else if width >= 2 {
		padLeft = 1
	}
	contentWidth := width - padLeft - padRight
	if contentWidth < 1 {
		contentWidth = 1
	}
	var out []string
	youStr := "You"
	avail := width - padLeft
	if ansi.StringWidth(youStr) > avail {
		youStr = truncateToWidth(youStr, avail, "")
	}
	rp := width - padLeft - ansi.StringWidth(youStr)
	if rp < 0 {
		rp = 0
	}
	out = append(out, userCardStyle.Render(strings.Repeat(" ", padLeft))+userRoleStyle.Render(youStr)+userCardStyle.Render(strings.Repeat(" ", rp)))
	if clean != "" {
		for _, line := range wrap(clean, contentWidth) {
			lw := ansi.StringWidth(line)
			if lw > contentWidth {
				line = truncateToWidth(line, contentWidth, "")
				lw = ansi.StringWidth(line)
			}
			rp = width - padLeft - lw
			if rp < 0 {
				rp = 0
			}
			out = append(out, userCardStyle.Render(strings.Repeat(" ", padLeft)+line+strings.Repeat(" ", rp)))
		}
	}
	return out
}

func renderAssistant(it presentation.FeedItem, width int) []string {
	text := it.Text
	if text == "" {
		text = "·"
	}
	clean := SanitizeForDisplay(text, DisplayPolicy{AllowNewline: true, AllowTab: true, Redact: false})
	return append([]string{bold.Render(green.Render("Nabd"))}, formatMarkdown(clean, width)...)
}

func renderTool(it presentation.FeedItem, width int, expanded ...bool) []string {
	if it.Tool == nil {
		return nil
	}
	if width <= 0 {
		width = DefaultWidth
	}
	t := it.Tool
	isExpanded := len(expanded) > 0 && expanded[0]
	out := []string{renderToolSummary(t, width)}
	if isExpanded {
		out = append(out, renderToolMetadata(t, width)...)
		if toolOutputAvailable(t) {
			if t.Name == "read_file" {
				out = append(out, renderReadFileOutput(t.Output, width)...)
			} else {
				out = append(out, truncateOutput(t.Output, width)...)
			}
		} else if t.Status == presentation.ToolRunning {
			out = append(out, dim.Render(truncateToWidth("  · running", width, "…")))
		}
	} else if (t.Status == presentation.ToolFailed || t.Status == presentation.ToolDenied) && t.Err != "" {
		clean := toolSummaryText(t.Err)
		if clean != "" {
			out = append(out, bad.Render(truncateToWidth("    · "+clean, width, "…")))
		}
	}
	return out
}

func toolDisplayName(name string) string {
	clean := toolSummaryText(name)
	switch clean {
	case "read_file":
		return "Read"
	case "write_file":
		return "Write"
	case "edit_file":
		return "Edit"
	case "bash":
		return "Bash"
	case "grep":
		return "Grep"
	case "glob":
		return "Glob"
	default:
		return clean
	}
}

func renderPerm(it presentation.FeedItem, width int) []string {
	if it.Perm == nil {
		return nil
	}
	p := it.Perm
	sym := "?"
	switch p.Status {
	case presentation.PermAllow:
		sym = good.Render("✓")
	case presentation.PermDeny:
		sym = bad.Render("✗")
	default:
		sym = warn.Render("?")
	}
	head := fmt.Sprintf("%s %s", sym, toolSummaryText(p.Name))
	if p.Args != "" {
		head += " " + truncate(toolSummaryText(p.Args), width/3)
	}
	out := []string{truncateToWidth(head, width, "…")}
	if p.Status == presentation.PermAllow && p.Effective != p.Decision {
		out = append(out, dim.Render(truncateToWidth(fmt.Sprintf("  · requested %s, applied %s", p.Decision, p.Effective), width, "…")))
	}
	return out
}

func renderNotice(it presentation.FeedItem, width int) []string {
	clean := SanitizeForDisplay(it.Text, DisplayPolicy{AllowNewline: true, Redact: true})
	lines := strings.Split(clean, "\n")
	out := make([]string, 0, len(lines))
	for i, raw := range lines {
		prefix := "  "
		if i == 0 {
			prefix = "⚑ "
		}
		out = append(out, warn.Render(truncateToWidth(prefix+raw, width, "…")))
	}
	return out
}
func renderError(it presentation.FeedItem, width int) []string {
	if it.Error != nil {
		return renderErrorCard(it.Error, width)
	}
	text := it.Text
	if text == "" {
		text = "error"
	}
	return []string{bad.Render(truncateToWidth("✗ "+toolSummaryText(text), width, "…"))}
}
func renderRunBoundary(it presentation.FeedItem, width int) []string {
	return []string{dim.Render(truncateToWidth("── "+SanitizeForDisplay(it.Text, DisplayPolicy{AllowNewline: false, Redact: false}), width, "…"))}
}
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	return truncateToWidth(s, max, "…")
}
