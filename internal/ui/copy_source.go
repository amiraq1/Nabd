package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"nabd/internal/presentation"
)

// copyRenderWidth is wide enough that renderItems never wraps a copied card,
// so a copied command or path keeps the line structure it had in the journal
// and credential redaction sees whole tokens instead of wrapped fragments.
const copyRenderWidth = 4096

// cardTextForCopyUnwrapped returns the plain text of card idx suitable for
// the copy pipeline. For tool cards it reads the full Output from the
// projection directly, bypassing the display-only char budget in truncateOutput.
// For all other card types it falls back to renderItems at copyRenderWidth.
// It never touches m.lines, m.offsets or the line cache.
func (m *Feed) cardTextForCopyUnwrapped(idx int) string {
	items := m.navigationItems()
	if idx < 0 || idx >= len(items) {
		return ""
	}
	it := items[idx]

	// Tool cards: assemble header + full output without the display char-budget.
	if it.Type == presentation.ItemTool && it.Tool != nil {
		return toolCardCopyText(it.Tool)
	}

	// All other card types: render at wide width so paths/URLs never wrap.
	lines := renderItems(items[idx:idx+1], copyRenderWidth, m.toolsExpanded)
	clean := make([]string, 0, len(lines))
	for _, l := range lines {
		clean = append(clean, strings.TrimRight(stripCardGutter(ansi.Strip(l)), " "))
	}
	return strings.Join(clean, "\n")
}

// toolCardCopyText assembles a human-readable copy payload for a tool card:
// one header line followed by the complete output, with ANSI stripped and
// display-sanitization applied but no char-budget imposed.
func toolCardCopyText(t *presentation.ToolCard) string {
	if t == nil {
		return ""
	}
	var b strings.Builder

	// Header: name and args summary.
	header := t.Name
	if t.Args != "" {
		header += " " + t.Args
	}
	b.WriteString(header)

	// Full output — sanitize for display (credential redaction + control-char
	// stripping) but do NOT truncate by char count.
	if t.Output != "" {
		sanitized := SanitizeForDisplay(t.Output, DisplayPolicy{
			AllowNewline: true,
			AllowTab:     true,
			Redact:       true,
		})
		if sanitized != "" {
			b.WriteByte('\n')
			b.WriteString(sanitized)
		}
	}
	return b.String()
}
