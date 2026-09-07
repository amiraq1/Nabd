package ui

import (
	"fmt"
	"strings"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

// renderItems converts feed items to display lines, each constrained to width.
func renderItems(items []presentation.FeedItem, width int, toolsExpanded ...bool) []string {
	lines, _ := renderItemsWithOffsets(items, width, toolsExpanded...)
	return lines
}

// renderItemsWithOffsets converts feed items to display lines and returns
// both the lines and the starting line index for each item.
func renderItemsWithOffsets(items []presentation.FeedItem, width int, toolsExpanded ...bool) ([]string, []int) {
	isExpanded := false
	if len(toolsExpanded) > 0 {
		isExpanded = toolsExpanded[0]
	}
	var lines []string
	offsets := make([]int, len(items))
	var prevIsMsg bool
	for i, it := range items {
		isMsg := it.Type == presentation.ItemUserMsg || it.Type == presentation.ItemAssistant
		if len(lines) > 0 && (isMsg || prevIsMsg) {
			lines = append(lines, "")
		}
		offsets[i] = len(lines)
		raw := renderItem(it, width, isExpanded)
		for _, l := range raw {
			// Guarantee every stored line fits within width terminal cells.
			if width > 0 && ansi.StringWidth(l) > width {
				lines = append(lines, strings.Split(ansi.Hardwrap(l, width, false), "\n")...)
			} else {
				lines = append(lines, l)
			}
		}
		prevIsMsg = isMsg
	}
	return lines, offsets
}

// renderItem renders one feed item to one or more lines.
func renderItem(it presentation.FeedItem, width int, toolsExpanded ...bool) []string {
	isExpanded := false
	if len(toolsExpanded) > 0 {
		isExpanded = toolsExpanded[0]
	}
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

	padLeft := 0
	padRight := 0
	if width >= 4 {
		padLeft = 1
		padRight = 1
	} else if width >= 2 {
		padLeft = 1
	}

	contentWidth := width - padLeft - padRight
	if contentWidth < 1 {
		contentWidth = 1
	}

	var out []string

	// Header line with "You" role label covering the full card width with background.
	youStr := "You"
	availForYou := width - padLeft
	if ansi.StringWidth(youStr) > availForYou {
		youStr = truncateToWidth(youStr, availForYou, "")
	}
	youWidth := ansi.StringWidth(youStr)
	rpYou := width - padLeft - youWidth
	if rpYou < 0 {
		rpYou = 0
	}
	headerLine := userCardStyle.Render(strings.Repeat(" ", padLeft)) +
		userRoleStyle.Render(youStr) +
		userCardStyle.Render(strings.Repeat(" ", rpYou))
	out = append(out, headerLine)

	if clean != "" {
		for _, l := range wrap(clean, contentWidth) {
			lw := ansi.StringWidth(l)
			if lw > contentWidth {
				l = truncateToWidth(l, contentWidth, "")
				lw = ansi.StringWidth(l)
			}
			rp := width - padLeft - lw
			if rp < 0 {
				rp = 0
			}
			cardLine := userCardStyle.Render(strings.Repeat(" ", padLeft) + l + strings.Repeat(" ", rp))
			out = append(out, cardLine)
		}
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
	out = append(out, bold.Render(green.Render("Nabd")))
	out = append(out, formatMarkdown(clean, width)...)
	return out
}

func renderTool(it presentation.FeedItem, width int, expanded ...bool) []string {
	if it.Tool == nil {
		return nil
	}
	isExpanded := false
	if len(expanded) > 0 {
		isExpanded = expanded[0]
	}
	if width <= 0 {
		width = DefaultWidth
	}
	t := it.Tool
	var out []string

	// 1. Single compact summary row
	out = append(out, renderToolSummary(t, width))

	// 2. Failure error preservation (if failed and has error)
	if (t.Status == presentation.ToolFailed || t.Status == presentation.ToolDenied) && t.Err != "" {
		cleanErr := SanitizeForDisplay(t.Err, DisplayPolicy{AllowNewline: false, Redact: true})
		if cleanErr != "" {
			errLine := "    " + bad.Render("· "+cleanErr)
			if ansi.StringWidth(errLine) > width {
				avail := width - 4 - 2 // 4 indent, 2 for "· "
				if avail > 0 {
					errLine = "    " + bad.Render("· "+truncateToWidth(cleanErr, avail, "…"))
				} else {
					errLine = truncateToWidth(errLine, width, "")
				}
			}
			out = append(out, errLine)
		}
	}

	// 3. Expanded output
	if isExpanded {
		if t.Output != "" {
			if t.Name == "read_file" {
				out = append(out, renderReadFileOutput(t.Output, width)...)
			} else {
				out = append(out, truncateOutput(t.Output, width)...)
			}
		} else if t.Status == presentation.ToolRunning {
			out = append(out, dim.Render("  ···"))
		}
	}

	return out
}

// toolDisplayName maps well-known tools to their presentation names.
// Unknown tools retain their clean/sanitized name intact.
func toolDisplayName(name string) string {
	clean := SanitizeForDisplay(name, DisplayPolicy{AllowNewline: false, Redact: true})
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

// renderToolSummary formats the compact single-line tool summary row:
// status symbol + tool display name + relevant argument + metadata.
func renderToolSummary(t *presentation.ToolCard, width int) string {
	sym := toolStatusSymbol(t.Status)
	name := toolDisplayName(t.Name)
	prefix := "  " + sym + " " + name
	prefixWidth := ansi.StringWidth("  " + "✓" + " " + name)

	// Build metadata parts: nonzero exit code / signal, then duration.
	var metaParts []string
	var metaPartsShort []string // fallback without duration if space is tight
	if t.ExitCode != 0 {
		metaParts = append(metaParts, bad.Render(fmt.Sprintf("exit %d", t.ExitCode)))
		metaPartsShort = append(metaPartsShort, bad.Render(fmt.Sprintf("exit %d", t.ExitCode)))
	}
	if t.Signal != "" {
		metaParts = append(metaParts, bad.Render(t.Signal))
		metaPartsShort = append(metaPartsShort, bad.Render(t.Signal))
	}
	if t.Duration > 0 {
		metaParts = append(metaParts, dim.Render(fmt.Sprintf("%dms", t.Duration)))
	}

	metaSep := dim.Render(" · ")
	metaStr := strings.Join(metaParts, metaSep)
	metaWidth := ansi.StringWidth(metaStr)

	cleanArgs := ""
	if t.Args != "" {
		cleanArgs = SanitizeForDisplay(t.Args, DisplayPolicy{AllowNewline: false, Redact: true})
	}

	// Try with full args and full metadata
	if cleanArgs != "" {
		metaOverhead := 0
		if metaWidth > 0 {
			metaOverhead = 3 + metaWidth // " · " + metaStr
		}
		availArgs := width - prefixWidth - 2 - metaOverhead
		if availArgs >= 4 {
			argStr := cleanArgs
			if ansi.StringWidth(argStr) > availArgs {
				argStr = truncateToWidth(argStr, availArgs, "…")
			}
			line := prefix + "  " + argStr
			if metaStr != "" {
				line += metaSep + metaStr
			}
			return line
		}
	}

	// If argument doesn't fit or doesn't exist, try prefix + metadata without argument
	if metaStr != "" {
		if prefixWidth+3+metaWidth <= width {
			return prefix + metaSep + metaStr
		}
		// Try without duration if exit/signal was present
		if len(metaPartsShort) > 0 && len(metaPartsShort) < len(metaParts) {
			metaShortStr := strings.Join(metaPartsShort, metaSep)
			if prefixWidth+3+ansi.StringWidth(metaShortStr) <= width {
				return prefix + metaSep + metaShortStr
			}
		}
	}

	// If metadata also doesn't fit, show prefix + argument if no metadata
	if cleanArgs != "" && metaStr == "" {
		availArgs := width - prefixWidth - 2
		if availArgs >= 4 {
			return prefix + "  " + truncateToWidth(cleanArgs, availArgs, "…")
		}
	}

	return truncateToWidth(prefix, width, "")
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
		return good.Render("✓")
	case presentation.ToolFailed:
		return bad.Render("✗")
	case presentation.ToolDenied:
		return bad.Render("✗")
	case presentation.ToolCancelled:
		return dim.Render("✗")
	default:
		return dim.Render("·")
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
