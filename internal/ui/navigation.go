package ui

import (
	"nabd/internal/presentation"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Feed) navigationItems() []presentation.FeedItem {
	items := mergeNotices(m.proj.Items(), m.notices)
	if len(items) > maxVisibleFeedItems {
		items = items[len(items)-maxVisibleFeedItems:]
	}
	return items
}

func (m *Feed) selectItem(index int) {
	items := m.navigationItems()
	if len(items) == 0 {
		m.selectedItem = -1
		return
	}
	if index < 0 { index = 0 }
	if index >= len(items) { index = len(items)-1 }
	m.selectedItem = index
	_, offsets := renderItemsWithOffsets(items, m.width, m.toolsExpanded)
	if index < len(offsets) {
		m.follow = false
		m.scrollTop = offsets[index]
		m.clampScroll()
	}
}

func (m *Feed) moveCard(delta int) {
	if m.selectedItem < 0 {
		if delta < 0 { m.selectedItem = len(m.navigationItems()) } else { m.selectedItem = -1 }
	}
	m.selectItem(m.selectedItem + delta)
}

func (m *Feed) selectType(kind presentation.ItemType, forward bool) {
	items := m.navigationItems()
	if len(items) == 0 { return }
	start, step := m.selectedItem, 1
	if !forward { step = -1 }
	for i := 1; i <= len(items); i++ {
		idx := (start + i*step) % len(items)
		if idx < 0 { idx += len(items) }
		if items[idx].Type == kind { m.selectItem(idx); return }
	}
	m.status = "no matching card"
}

func (m *Feed) enterNavigation() {
	m.navigationMode = true
	m.composer.blur()
	m.status = "j/k cards · Enter expand · n error · p permission · g/G ends · Esc input"
	if m.selectedItem < 0 { m.selectItem(len(m.navigationItems())-1) }
}

func (m *Feed) navigationKey(k tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch k.Type {
	case tea.KeyUp:
		m.moveCard(-1); return m, nil, true
	case tea.KeyDown:
		m.moveCard(1); return m, nil, true
	case tea.KeyEnter:
		items := m.navigationItems()
		if m.selectedItem >= 0 && m.selectedItem < len(items) && items[m.selectedItem].Type == presentation.ItemTool {
			model, cmd := m.toggleTools(); return model, cmd, true
		}
		return m, nil, true
	case tea.KeyEsc:
		m.navigationMode = false; m.status = ""; m.composer.focus(); return m, nil, true
	}
	if k.Paste { return m, nil, false }
	switch k.String() {
	case "j": m.moveCard(1); return m, nil, true
	case "k": m.moveCard(-1); return m, nil, true
	case "g": m.selectItem(0); return m, nil, true
	case "G": m.selectItem(len(m.navigationItems())-1); m.follow = true; m.scrollToEnd(); return m, nil, true
	case "n": m.selectType(presentation.ItemError, true); return m, nil, true
	case "p": m.selectType(presentation.ItemPermission, false); return m, nil, true
	case "?":
		if m.status == "" { m.status = "j/k cards · Enter expand · n error · p permission · g/G ends · Esc input" } else { m.status = "" }
		return m, nil, true
	}
	return m, nil, false
}
