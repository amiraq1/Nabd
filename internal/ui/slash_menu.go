package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// maxMenuCommands is the maximum number of items displayed in the menu.
const maxMenuCommands = 8

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

func (m *slashMenu) lineCount() int {
	if !m.visible || len(m.items) == 0 {
		return 0
	}
	// 1 border header + len(items) + 1 bottom border
	return len(m.items) + 2
}

// maxItemsForHeight returns the maximum number of menu items that fit in
// a given available height (accounting for 2 border rows).
func maxItemsForHeight(availableRows int) int {
	if availableRows <= 2 {
		return 0
	}
	n := availableRows - 2
	if n > maxMenuCommands {
		return maxMenuCommands
	}
	return n
}

// view renders the menu popup docked above the composer.
func (m *slashMenu) view(width int) string {
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

	var b strings.Builder
	b.WriteString(dim.Render(headerLine))
	b.WriteByte('\n')

	for i, cmd := range m.items {
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
