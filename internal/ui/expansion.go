package ui

import "nabd/internal/presentation"

// effectiveExpanded reports whether the item with the given ID should be
// rendered in expanded form. An explicit per-card override takes precedence;
// if absent, it follows the global toolsExpanded default.
func (m *Feed) effectiveExpanded(id string) bool {
	if v, ok := m.overrides[id]; ok {
		return v
	}
	return m.toolsExpanded
}

// toggleCard toggles the expansion state of the item at idx.
// Returns true if the item's expansion state changed.
func (m *Feed) toggleCard(idx int) bool {
	items := m.navigationItems()
	if idx < 0 || idx >= len(items) {
		return false
	}
	it := items[idx]
	if !expandable(it) {
		return false
	}
	if m.overrides == nil {
		m.overrides = make(map[string]bool, 4)
	}
	m.overrides[it.ID] = !m.effectiveExpanded(it.ID)
	return true
}

// expandable reports whether an item has collapsed detail worth revealing.
// Only tool cards render differently when expanded (see renderTool).
func expandable(it presentation.FeedItem) bool {
	return it.Type == presentation.ItemTool && it.ID != "" && it.Tool != nil
}

// pruneOverrides drops overrides for cards no longer in the feed, so the
// map cannot grow without bound across a long session. Called from refresh.
func (m *Feed) pruneOverrides(items []presentation.FeedItem) {
	if len(m.overrides) == 0 {
		return
	}
	live := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it.ID != "" {
			live[it.ID] = struct{}{}
		}
	}
	for id := range m.overrides {
		if _, ok := live[id]; !ok {
			delete(m.overrides, id)
		}
	}
}

// refreshPreservingSelection re-renders the feed and pins scrollTop to the
// top of the currently selected card so height changes do not shift it out of view.
func (m *Feed) refreshPreservingSelection() {
	m.refresh()
	if m.selectedItem >= 0 && m.selectedItem < len(m.offsets) {
		m.scrollTop = m.offsets[m.selectedItem]
		m.clampScroll()
	}
}
