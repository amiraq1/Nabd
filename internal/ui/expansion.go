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

// toolIsRunning is a pure predicate on the item already in hand.
func toolIsRunning(it presentation.FeedItem) bool {
	return it.Type == presentation.ItemTool && it.Tool != nil &&
		it.Tool.Status == presentation.ToolRunning
}

// expansionOf reports whether the item should be rendered in expanded form.
// An explicit per-card override takes precedence; if absent or expandDefault,
// it follows global toolsExpanded or the live lifecycle default (running tools
// are expanded by default until ToolEnd).
//
// Lifecycle precedence: a running tool card remains expanded by default even
// when global toolsExpanded is false (e.g. after Ctrl+O), because its live
// execution lifecycle governs its visibility until it finishes. A user wishing
// to collapse a running tool specifically can toggle the card directly, which
// records an explicit expandCollapsed override.
func (m *Feed) expansionOf(it presentation.FeedItem) bool {
	switch m.overrides[it.ID] {
	case expandOpened:
		return true
	case expandCollapsed:
		return false
	}
	return m.toolsExpanded || toolIsRunning(it)
}

// effectiveExpanded reports whether the item with the given ID should be
// rendered in expanded form. It is a convenience wrapper for callers that
// only hold an item ID (such as navigation, tests, and pointer handlers).
// In hot render loops (renderItemsCached), expansionOf(it) is used directly
// to avoid looking up the item in m.proj.
func (m *Feed) effectiveExpanded(id string) bool {
	switch m.overrides[id] {
	case expandOpened:
		return true
	case expandCollapsed:
		return false
	}
	if m.toolsExpanded {
		return true
	}
	if m.proj != nil {
		for _, it := range m.proj.Items() {
			if it.ID == id {
				return toolIsRunning(it)
			}
		}
	}
	return false
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
