package ui

import "nabd/internal/presentation"

// expandState represents the tri-state per-card expansion state.
// expandDefault: card follows its automatic lifecycle (or global toolsExpanded).
// expandOpened: card was explicitly expanded by user action.
// expandCollapsed: card was explicitly collapsed by user action.
type expandState int

const (
	expandDefault expandState = iota
	expandOpened
	expandCollapsed
)

// isToolRunning reports whether the item with id is an active running tool.
func (m *Feed) isToolRunning(id string) bool {
	if m.proj == nil {
		return false
	}
	for _, it := range m.proj.Items() {
		if it.ID == id && it.Type == presentation.ItemTool && it.Tool != nil {
			return it.Tool.Status == presentation.ToolRunning
		}
	}
	return false
}

// effectiveExpanded reports whether the item with the given ID should be
// rendered in expanded form. An explicit per-card override takes precedence;
// if absent or expandDefault, it follows the global toolsExpanded default,
// except for currently running tools which are expanded by default and
// auto-collapse upon ToolEnd.
func (m *Feed) effectiveExpanded(id string) bool {
	if v, ok := m.overrides[id]; ok {
		switch v {
		case expandOpened:
			return true
		case expandCollapsed:
			return false
		case expandDefault:
			// fall through to default lifecycle
		}
	}
	if m.toolsExpanded {
		return true
	}
	return m.isToolRunning(id)
}

// cardExpansion returns the expansion state for item id.
func (m *Feed) cardExpansion(id string) expandState {
	if v, ok := m.overrides[id]; ok && v != expandDefault {
		return v
	}
	if m.toolsExpanded || m.isToolRunning(id) {
		return expandOpened
	}
	return expandDefault
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
		m.overrides = make(map[string]expandState, 4)
	}
	if m.effectiveExpanded(it.ID) {
		m.overrides[it.ID] = expandCollapsed
	} else {
		m.overrides[it.ID] = expandOpened
	}
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
