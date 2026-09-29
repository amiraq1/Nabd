package ui

import (
	"fmt"
	"strings"

	"nabd/internal/presentation"

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
	if text, ok := logicalCardText(items[idx]); ok {
		return text
	}
	lines := renderItems(items[idx:idx+1], copyRenderWidth, m.expansionOf(items[idx]))
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = strings.TrimRight(stripCardGutter(ansi.Strip(l)), " ")
	}
	return strings.Join(clean, "\n")
}

// logicalCardText returns copy/search text directly from canonical projection
// fields for human-language cards. It deliberately bypasses visual clusters:
// mirrored delimiters and reordered Arabic must never enter the clipboard.
// Tool cards return false because their structured summary/output renderer is
// also their established redacted copy contract; tool output remains an LTR
// code-owned region in this integration.
func logicalCardText(it presentation.FeedItem) (string, bool) {
	switch it.Type {
	case presentation.ItemUserMsg:
		return strings.TrimRight("You\n"+it.Text, "\n"), true
	case presentation.ItemAssistant:
		return strings.TrimRight("Nabd\n"+it.Text, "\n"), true
	case presentation.ItemNotice:
		return it.Text, true
	case presentation.ItemRunBoundary:
		return it.Text, true
	case presentation.ItemPermission:
		if it.Perm == nil {
			return "", true
		}
		p := it.Perm
		var b strings.Builder
		b.WriteString(p.Name)
		if p.Args != "" {
			b.WriteByte(' ')
			b.WriteString(p.Args)
		}
		if p.Reason != "" {
			b.WriteByte('\n')
			b.WriteString(p.Reason)
		}
		return b.String(), true
	case presentation.ItemError:
		if it.Error == nil {
			return it.Text, true
		}
		return fmt.Sprintf("%s\n%s\n%s", it.Error.Title, it.Error.Message, it.Error.ActionText), true
	default:
		return "", false
	}
}
