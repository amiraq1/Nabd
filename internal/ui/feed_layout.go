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
 feature/nbd-104-rot
// It returns true when the final rendered output (m.lines) actually changed,
// and false when it is byte-for-byte identical to the previous refresh.
// The detection uses a deterministic fingerprint of the rendered lines, so
// callers no longer need to clone and compare the slice themselves.
func (m *Feed) refresh() bool {

func (m *Feed) refresh() {
 master
	items := mergeNotices(m.proj.Items(), m.notices)
	// DOCUMENTED DECISION: Vertical trimming at maxVisibleFeedItems shifts the
	// anchor under from-top index convention when buffer exceeds the cap.
	if len(items) > maxVisibleFeedItems {
		items = items[len(items)-maxVisibleFeedItems:]
	}
 feature/nbd-104-rot

	// Invalidate entire cache on width change.
	if m.width != m.cacheWidth {
		m.lineCache = nil
		m.cacheWidth = m.width
	}

	m.lines = renderItemsCached(m, items, m.width, m.toolsExpanded)

	// Evict cache entries for items no longer in the feed.
	if m.lineCache != nil {
		active := make(map[string]bool, len(items))
		for _, it := range items {
			if it.ID != "" {
				active[it.ID] = true
			}
		}
		for id := range m.lineCache {
			if !active[id] {
				delete(m.lineCache, id)
			}
		}
	}

	// Derive dirty from the final rendered output fingerprint.
	nextSig := renderedLinesFingerprint(m.lines)
	nextRows := len(m.lines)
	dirty := !m.renderSigValid ||
		m.renderRows != nextRows ||
		m.renderSig != nextSig

	// Always keep the signature in sync, even when dirty == false, so the
	// next refresh compares against this finalized output.
	m.renderSig = nextSig
	m.renderRows = nextRows
	m.renderSigValid = true

	m.clampScroll()
	return dirty
}

// renderedLinesFingerprint returns a deterministic FNV-1a (64-bit) hash of the
// rendered line slice. It starts by encoding the line count as a little-endian
// uint64, then for each line its byte length (same encoding) followed by the
// line bytes. Length-prefixing removes line-boundary ambiguity (["ab","c"]
// differs from ["a","bc"]); it does not make collisions impossible.
func renderedLinesFingerprint(lines []string) uint64 {
	const offset64 uint64 = 14695981039346656037
	var prime64 uint64 = 1099511628211
	h := offset64
	var buf [8]byte

	// Line count.
	putUint64LE(buf[:], uint64(len(lines)))
	for _, b := range buf {
		h = (h ^ uint64(b)) * prime64
	}

	for _, s := range lines {
		putUint64LE(buf[:], uint64(len(s)))
		for _, b := range buf {
			h = (h ^ uint64(b)) * prime64
		}
		for i := 0; i < len(s); i++ {
			h = (h ^ uint64(s[i])) * prime64
		}
	}
	return h
}

// putUint64LE writes v as 8 little-endian bytes into dst (len >= 8).
func putUint64LE(dst []byte, v uint64) {
	for i := 0; i < 8; i++ {
		dst[i] = byte(v >> (8 * i))
	}
}

// syncRenderSig recomputes the render signature from the current m.lines.
// Callers that write m.lines outside refresh (e.g. toggleTools' direct
// line-render path) must invoke it so the next refresh compares against
// the actual current output, not a stale signature.
func (m *Feed) syncRenderSig() {
	m.renderSig = renderedLinesFingerprint(m.lines)
	m.renderRows = len(m.lines)
	m.renderSigValid = true

	m.lines = renderItems(items, m.width, m.toolsExpanded)
	m.clampScroll()
 master
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
