package ui

import (
	"fmt"

	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

// This file owns layout, scrolling, and the notice/diagnostic feed lines.
// Pure moves out of feed.go: no behaviour changes.

// hasTools reports whether any tool item exists in the feed.
func (m *Feed) hasTools() bool {
	for _, it := range m.proj.Items() {
		if it.Type == presentation.ItemTool {
			return true
		}
	}
	return false
}

// addDiagnostic records a UI-side diagnostic (not written to journal).
func (m *Feed) addDiagnostic(text string) {
	m.diagnostics = append(m.diagnostics, text)
	if len(m.diagnostics) > maxUIDiagnostics {
		m.diagnostics = m.diagnostics[len(m.diagnostics)-maxUIDiagnostics:]
	}
}

// addNotice appends a permanent UI notice to the feed through the same
// render path as journal items. It is anchored after the last event Seq
// seen, so later journal events render below it. The feed is refreshed and
// follows to the end when the user was already following.
func (m *Feed) addNotice(kind presentation.ItemType, text string) {
	if text == "" {
		return
	}
	m.notices = append(m.notices, presentation.FeedItem{
		Type: kind,
		ID:   fmt.Sprintf("ui_%d_%d", m.lastSeq, len(m.notices)),
		Seq:  m.lastSeq,
		Text: text,
	})
	if len(m.notices) > maxUINotices {
		m.notices = m.notices[len(m.notices)-maxUINotices:]
	}
	m.refresh()
	if m.follow && !m.modalVisible && !m.decisionPending {
		m.scrollToEnd()
	} else {
		m.unseen++
	}
}

// mergeNotices interleaves UI notices into the Seq-sorted projector items.
// A notice anchored at Seq k is placed after every item with Seq <= k.
// Notices are appended in chronological order with non-decreasing anchors,
// so a single forward merge keeps both sequences in order.
func mergeNotices(items, notices []presentation.FeedItem) []presentation.FeedItem {
	if len(notices) == 0 {
		return items
	}
	out := make([]presentation.FeedItem, 0, len(items)+len(notices))
	ni := 0
	for _, it := range items {
		for ni < len(notices) && notices[ni].Seq < it.Seq {
			out = append(out, notices[ni])
			ni++
		}
		out = append(out, it)
	}
	out = append(out, notices[ni:]...)
	return out
}

// bottomStart returns the canonical index of the first visible line when
// the viewport is anchored at the bottom (newest rendered line).
func (m *Feed) bottomStart(vh int) int {
	if vh <= 0 || len(m.lines) <= vh {
		return 0
	}
	return len(m.lines) - vh
}

// clampScroll enforces canonical bounds: 0 <= scrollTop <= bottomStart.
// If follow is active, it re-anchors scrollTop to bottomStart and clears unseen.
func (m *Feed) clampScroll() {
	lm := m.computeLayout()
	bs := m.bottomStart(lm.ViewportRows)
	if m.follow && !m.modalVisible && !m.decisionPending {
		m.scrollTop = bs
		m.unseen = 0
		return
	}
	if m.scrollTop < 0 {
		m.scrollTop = 0
	}
	if m.scrollTop > bs {
		m.scrollTop = bs
	}
}

// refresh rebuilds the visible lines from the projector plus UI notices.
func (m *Feed) refresh() {
	items := mergeNotices(m.proj.Items(), m.notices)
	// DOCUMENTED DECISION: Vertical trimming at maxVisibleFeedItems shifts the
	// anchor under from-top index convention when buffer exceeds the cap.
	if len(items) > maxVisibleFeedItems {
		items = items[len(items)-maxVisibleFeedItems:]
	}
	m.lines = renderItems(items, m.width, m.toolsExpanded)
	m.clampScroll()
}

// scrollToEnd moves the viewport to show the latest items and re-arms follow.
func (m *Feed) scrollToEnd() {
	m.follow = true
	m.unseen = 0
	lm := m.computeLayout()
	m.scrollTop = m.bottomStart(lm.ViewportRows)
}

// onResize recomputes the layout: composer first, then the viewport. Focus
// and text are preserved; nothing goes negative.
func (m *Feed) onResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	if m.width < minViewportWidth {
		m.width = minViewportWidth
	}
	m.height = msg.Height
	if m.height < 1 {
		m.height = 1
	}
	m.composer.resize(m.width, maxComposerHeight)
	m.syncSlashMenu()
	m.refresh()
	m.clampScroll()
	return m, nil
}

// viewportHeight returns the number of rows the viewport may use.
// Delegates to computeLayout for accurate visual-row accounting.
// Kept for backward compatibility with tests.
func (m *Feed) viewportHeight() int {
	return m.computeLayout().ViewportRows
}
