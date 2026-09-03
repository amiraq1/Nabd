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
		items:    nil,
		visible:  false,
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

// view renders the menu popup docked above the composer.
func (m *slashMenu) view(width int) string {
	if !m.visible || len(m.items) == 0 {
		return ""
	}

	w := width
	if w < 10 {
		w = 10
	}
	menuW := w
	if menuW > 50 {
		menuW = 50
	}

	var b strings.Builder
	title := "── Commands "
	if ansi.StringWidth(title) > menuW-2 {
		title = "── "
	}
	topDash := max(0, menuW-ansi.StringWidth(title))
	b.WriteString(dim.Render(title + strings.Repeat("─", topDash)))
	b.WriteByte('\n')

	avail := max(1, menuW-4)
	for i, cmd := range m.items {
		prefix := "  "
		line := fmt.Sprintf("%-12s %s", cmd.Usage, cmd.Description)
		if ansi.StringWidth(line) > avail {
			line = ansi.Truncate(line, avail, "…")
		}
		if i == m.selected {
			prefix = "> "
			b.WriteString(good.Render(prefix + line))
		} else {
			b.WriteString(dim.Render(prefix + line))
		}
		b.WriteByte('\n')
	}

	b.WriteString(dim.Render(strings.Repeat("─", menuW)))
	return b.String()
}
