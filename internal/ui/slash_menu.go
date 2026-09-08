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

// slashMenuShape is the single source of truth for how many terminal rows the
// menu reserves and which slice of items it renders. Both lineCount (the
// reservation that computeLayout relies on) and view share it, so the
// reservation and the rendered row count can never diverge — mirroring the
// guarantee the permission modal already makes through its own shape().
type slashMenuShape struct {
	rows  int
	start int
	end   int
}

func (m *slashMenu) shape(maxRows ...int) slashMenuShape {
	if !m.visible || len(m.items) == 0 {
		return slashMenuShape{}
	}
	full := len(m.items) + 2
	rows := full
	if len(maxRows) > 0 && maxRows[0] > 0 {
		rows = maxRows[0]
	}
	if rows > full {
		rows = full
	}
	if rows < 2 {
		rows = 2
	}
	itemRows := rows - 2
	start, end := 0, 0
	if itemRows > 0 && len(m.items) > itemRows {
		start = m.selected - itemRows/2
		if start < 0 {
			start = 0
		}
		if start+itemRows > len(m.items) {
			start = len(m.items) - itemRows
			if start < 0 {
				start = 0
			}
		}
	}
	end = min(start+itemRows, len(m.items))
	return slashMenuShape{rows: rows, start: start, end: end}
}

func (m *slashMenu) lineCount(maxRows ...int) int {
	return m.shape(maxRows...).rows
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

	s := m.shape(maxRows...)

	var b strings.Builder
	b.WriteString(dim.Render(headerLine))
	b.WriteByte('\n')

	for i := s.start; i < s.end; i++ {
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
