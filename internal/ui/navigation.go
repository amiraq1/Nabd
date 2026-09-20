package ui

import (
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Feed) navigationItems() []presentation.FeedItem {
	return visibleFeedItems(mergeNotices(m.proj.Items(), m.notices))
}

// selectItemInPlace updates the selected card index and repaints the feed
// so the gutter marker moves, but leaves the viewport scroll position
// untouched. Pointer taps use this because the tapped card is already
// on screen; keyboard navigation uses selectItem, which scrolls the card
// into view.
func (m *Feed) selectItemInPlace(index int) {
	items := m.navigationItems()
	if len(items) == 0 {
		m.selectedItem = -1
		return
	}
	if index < 0 {
		index = 0
	}
	if index >= len(items) {
		index = len(items) - 1
	}
	prev := m.selectedItem
	m.selectedItem = index

	// The gutter marker lives inside m.lines, so changing the selection is a
	// render change, not just a scroll change. Two cards repaint (the one
	// losing the marker, the one gaining it); the warm line cache serves the
	// rest, which is what keeps this affordable on every keystroke.
	//
	// The length check stays as a defensive resync: a mismatch means offsets
	// predate the current feed.
	if prev != index || len(m.offsets) != len(items) {
		m.refresh()
	}
}

func (m *Feed) selectItem(index int) {
	m.selectItemInPlace(index)
	if m.selectedItem >= 0 && m.selectedItem < len(m.offsets) {
		m.follow = false
		m.scrollTop = m.offsets[m.selectedItem]
		m.clampScroll()
	}
}

func (m *Feed) moveCard(delta int) {
	if m.selectedItem < 0 {
		if delta < 0 {
			m.selectedItem = len(m.navigationItems())
		} else {
			m.selectedItem = -1
		}
	}
	m.selectItem(m.selectedItem + delta)
}

func (m *Feed) selectType(kind presentation.ItemType, forward bool) {
	items := m.navigationItems()
	if len(items) == 0 {
		return
	}
	start, step := m.selectedItem, 1
	if !forward {
		step = -1
	}
	for i := 1; i <= len(items); i++ {
		idx := (start + i*step) % len(items)
		if idx < 0 {
			idx += len(items)
		}
		if items[idx].Type == kind {
			m.selectItem(idx)
			return
		}
	}
	m.setStatus("no matching card", rankHint)
}

func (m *Feed) enterNavigation() {
	m.navigationMode = true
	m.composer.blur()
	if m.selectedItem < 0 {
		m.selectItem(len(m.navigationItems()) - 1)
	}
	m.refresh()
}

func (m *Feed) navigationKey(k tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch k.Type {
	case tea.KeyUp:
		m.moveCard(-1)
		return m, nil, true
	case tea.KeyDown:
		m.moveCard(1)
		return m, nil, true
	case tea.KeyHome:
		// Home/End are navigation keys: while the composer owns focus they
		// scroll the viewport (viewportKey), and only in navigation mode do
		// they jump the card selection. Esc is therefore required first.
		m.selectItem(0)
		return m, nil, true
	case tea.KeyEnd:
		m.selectItem(len(m.navigationItems()) - 1)
		m.follow = true
		m.scrollToEnd()
		return m, nil, true
	case tea.KeyEsc:
		m.navigationMode = false
		m.clearStatus()
		m.composer.focus()
		m.refresh()
		return m, nil, true
	}
	if k.Paste {
		return m, nil, false
	}
	switch k.String() {
	case "enter", " ", "space":
		// Expansion only reveals already-projected output. It never
		// executes a tool and never answers a permission prompt: the
		// modal owns its own key routing, ahead of navigation.
		if m.toggleCard(m.selectedItem) {
			m.refreshPreservingSelection()
			return m, nil, true
		}
		// Swallowing the key silently reads as a frozen UI; say why.
		m.setStatus("this card has no collapsed output", rankHint)
		return m, nil, true
	case "j":
		m.moveCard(1)
		return m, nil, true
	case "k":
		m.moveCard(-1)
		return m, nil, true
	case "g":
		m.selectItem(0)
		return m, nil, true
	case "G":
		m.selectItem(len(m.navigationItems()) - 1)
		m.follow = true
		m.scrollToEnd()
		return m, nil, true
	case "n":
		m.selectType(presentation.ItemError, true)
		return m, nil, true
	case "p":
		m.selectType(presentation.ItemPermission, false)
		return m, nil, true
	case "d":
		m.setCommandResult(m.Diagnostics(m.width))
		return m, nil, true
	case "/":
		m, cmd := m.enterSearch()
		return m, cmd, true
	case "y", "c":
		m, cmd := m.copySelectedCard()
		return m, cmd, true
	case "Y":
		// Uppercase Y copies the whole report (all cards, tools expanded).
		m, cmd := m.copyFullReport()
		return m, cmd, true
	case "?":
		if m.status == "" {
			m.setStatus(navigationHint(m.width), rankHint)
		} else {
			m.clearStatus()
		}
		return m, nil, true
	}
	return m, nil, false
}
