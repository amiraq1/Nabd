package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// copyRenderWidth is wide enough that renderItems never wraps a copied card,
// so a copied command or path keeps the line structure it had in the journal
// and credential redaction sees whole tokens instead of wrapped fragments.
const copyRenderWidth = 4096

// cardTextForCopyUnwrapped renders one projected card at copyRenderWidth.
// It reads only the projection, never the raw journal, and never touches
// m.lines, m.offsets or the line cache.
func (m *Feed) cardTextForCopyUnwrapped(idx int) string {
	items := m.navigationItems()
	if idx < 0 || idx >= len(items) {
		return ""
	}
	lines := renderItems(items[idx:idx+1], copyRenderWidth, m.expansionOf(items[idx]))
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = strings.TrimRight(stripCardGutter(ansi.Strip(l)), " ")
	}
	return strings.Join(clean, "\n")
}
