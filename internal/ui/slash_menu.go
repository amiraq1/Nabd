package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// maxMenuCommands is the maximum number of items displayed in the menu.

// slashMenu manages state and rendering of the slash command completion menu.
type slashMenu struct {
	visible  bool
	items    []SlashCommand
	selected int
}

func newSlashMenu() *slashMenu {
	return &slashMenu{
		selected: 0,
	}
}

func (m *slashMenu) open(items []SlashCommand) {
	m.visible = true
	m.items = items
	if m.selected >= len(items) || m.selected < 0 {
		m.selected = 0
	}
}

func (m *slashMenu) close() {
	m.visible = false
	m.items = nil
	m.selected = 0
}

func (m *slashMenu) next() {
	if len(m.items) == 0 {
		return
	}
	m.selected = (m.selected + 1) % len(m.items)
}

func (m *slashMenu) prev() {
	if len(m.items) == 0 {
		return
	}
	m.selected = (m.selected - 1 + len(m.items)) % len(m.items)
}

func (m *slashMenu) currentCommand() (SlashCommand, bool) {
	if !m.visible || len(m.items) == 0 || m.selected < 0 || m.selected >= len(m.items) {
		return SlashCommand{}, false
	}
	return m.items[m.selected], true
}

func (m *slashMenu) lineCount(maxRows ...int) int {
	if !m.visible || len(m.items) == 0 {
		return 0
	}
	full := len(m.items) + 2
	if len(maxRows) > 0 && maxRows[0] > 0 && maxRows[0] < full {
		return max(2, maxRows[0])
	}
	return full
}

// view renders the menu popup docked above the composer.
func (m *slashMenu) view(width int, maxRows ...int) string {
	if !m.visible || len(m.items) == 0 {
		return ""
	}

	w := width
	if w < 20 {
		w = 20
	}
	// Menu is at most 50 wide on narrow phones, full width on wider screens.
	menuW := w
	if menuW > 50 {
		menuW = 50
	}

	// Build separator line that exactly fills menuW (never auto-wraps).
	// Header: "── Commands ─────────"
	header := "── Commands "
	headerW := ansi.StringWidth(header)
	dashesNeeded := menuW - headerW
	if dashesNeeded < 0 {
		dashesNeeded = 0
	}
	headerLine := header + strings.Repeat("─", dashesNeeded)

	// Footer separator.
	footerLine := strings.Repeat("─", menuW)

	targetRows := m.lineCount(maxRows...)
	maxItemRows := targetRows - 2
	if maxItemRows < 1 {
		maxItemRows = 1
	}

	start := 0
	if len(m.items) > maxItemRows {
		start = m.selected - maxItemRows/2
		if start < 0 {
			start = 0
		}
		if start+maxItemRows > len(m.items) {
			start = len(m.items) - maxItemRows
			if start < 0 {
				start = 0
			}
		}
	}
	end := min(start+maxItemRows, len(m.items))

	var b strings.Builder
	b.WriteString(dim.Render(headerLine))
	b.WriteByte('\n')

	for i := start; i < end; i++ {
		cmd := m.items[i]
		prefix := "  "
		line := fmt.Sprintf("%-12s %s", cmd.Usage, cmd.Description)
		maxLineW := menuW - ansi.StringWidth(prefix)
		if maxLineW < 4 {
			maxLineW = 4
		}
		if ansi.StringWidth(line) > maxLineW {
			line = ansi.Truncate(line, maxLineW, "…")
		}
		if i == m.selected {
			prefix = "> "
			b.WriteString(good.Render(prefix + line))
		} else {
			b.WriteString(dim.Render(prefix + line))
		}
		b.WriteByte('\n')
	}

	b.WriteString(dim.Render(footerLine))
	return b.String()
}
