package ui

import (
	"fmt"
	"sort"

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

// addErrorNotice appends a permanent error notice carrying a structured ErrorCard
// to the feed, rendering with the 4 error card fields (code/wait/details/action).
func (m *Feed) addErrorNotice(card *presentation.ErrorCard, text string) {
	if card == nil && text == "" {
		return
	}
	m.notices = append(m.notices, presentation.FeedItem{
		Type:  presentation.ItemError,
		ID:    fmt.Sprintf("ui_%d_%d", m.lastSeq, len(m.notices)),
		Seq:   m.lastSeq,
		Text:  text,
		Error: card,
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

// viewportTopPadding returns the number of blank rows to place before the
// feed content in the viewport, so short conversations sit at the bottom of
// the screen instead of floating at the top with dead space below. Shared by
// View (which emits the padding) and pointerLine (which must skip it) so
// touch coordinates stay consistent with the rendering.
//
// It depends on the rendered content length and the viewport height only, NOT
// on m.follow: making it follow-dependent meant that pressing PgUp on short
// content (which clears follow) collapsed the padding to zero and the content
// jumped upward, although there was nothing to scroll. When content is shorter
// than the viewport its canonical scroll position is already 0, so a stable
// padding is always correct.
func (m *Feed) viewportTopPadding(lm layoutMetrics) int {
	if m.modalVisible || m.decisionPending {
		return 0
	}
	if len(m.lines) >= lm.ViewportRows {
		return 0
	}
	return lm.ViewportRows - len(m.lines)
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
// It returns true when the final rendered output (m.lines) actually changed,
// and false when it is byte-for-byte identical to the previous refresh.
//
// Fingerprinting is incremental: the projector reports exactly which items
// the incoming batch touched, so only those items are re-hashed (L15);
// every other item reuses its cached fingerprint. When no fingerprint
// changed and the structural signature (width, expansion, selection,
// notices, item count) matches the last full render, the rendered lines are
// provably identical: refresh() rebuilds them from the warm line cache with
// plain copies (no re-render, no content hashing) and returns false without
// hashing the output again.
func (m *Feed) refresh() bool {
	items := visibleFeedItems(mergeNotices(m.proj.Items(), m.notices))

	changed := m.syncFingerprints(items)
	sig := m.structSig(items)
	if !changed && m.structSigValid && sig == m.lastStructSig && m.renderSigValid {
		// Nothing feeding the renderer changed: rebuild m.lines from the
		// warm line cache (cheap copies — no re-render, no re-fingerprint)
		// and report unchanged without hashing the output again.
		m.lines, m.offsets = renderItemsCached(m, items, m.width, m.toolsExpanded)
		m.clampScroll()
		return false
	}

	// Invalidate entire cache on width change.
	if m.width != m.cacheWidth {
		m.lineCache = nil
		m.cacheWidth = m.width
	}

	m.lines, m.offsets = renderItemsCached(m, items, m.width, m.toolsExpanded)
	m.pruneOverrides(items)

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
	// Recompute after pruneOverrides: the stored signature must reflect the
	// final render state, while the fast-path check above (correctly) used
	// the pre-prune signature.
	m.lastStructSig = m.structSig(items)
	m.structSigValid = true

	m.clampScroll()
	return dirty
}

// syncFingerprints drains the projector's touched set and (re)computes
// fingerprints only for touched or unseen items; everything else reuses the
// cached value. It evicts cache entries for items that left the feed and
// reports whether any fingerprint differs from the previous refresh.
func (m *Feed) syncFingerprints(items []presentation.FeedItem) (changed bool) {
	if m.fpCache == nil {
		m.fpCache = make(map[presentation.ItemKey]uint64)
	}
	var touched map[presentation.ItemKey]bool
	if m.proj != nil {
		if ids := m.proj.DrainTouched(); len(ids) > 0 {
			touched = make(map[presentation.ItemKey]bool, len(ids))
			for _, id := range ids {
				touched[id] = true
			}
		}
	}
	active := make(map[presentation.ItemKey]bool, len(items))
	for _, it := range items {
		if it.ID == "" {
			// ID-less items are never cached by the render path either;
			// treat them as always changed (cheap: they are tiny).
			changed = true
			continue
		}
		key := it.Key()
		active[key] = true
		oldFP, ok := m.fpCache[key]
		if !ok || touched[key] {
			fp := it.Fingerprint()
			if !ok || fp != oldFP {
				changed = true
			}
			m.fpCache[key] = fp
		}
	}
	for key := range m.fpCache {
		if !active[key] {
			delete(m.fpCache, key)
		}
	}
	return changed
}

// fpOf returns the item's fingerprint, preferring the cache populated by
// syncFingerprints. A miss computes it directly, so render paths that run
// without a preceding sync (tests, one-off renders) stay correct.
func (m *Feed) fpOf(it presentation.FeedItem) uint64 {
	if it.ID != "" && m.fpCache != nil {
		if fp, ok := m.fpCache[it.Key()]; ok {
			return fp
		}
	}
	return it.Fingerprint()
}

// structSig hashes the non-content inputs of the render pipeline: width,
// expansion state (global flag plus the full per-card overrides map),
// selection, and item count. Item content is covered by the fingerprint
// cache; the projector's touched set guarantees content changes surface
// through syncFingerprints. The overrides map is hashed by content (sorted
// keys) so every mutation path — including direct test writes — is
// reflected; it only ever holds a handful of entries.
func (m *Feed) structSig(items []presentation.FeedItem) uint64 {
	const offset64 uint64 = 14695981039346656037
	const prime64 uint64 = 1099511628211
	h := offset64
	mix := func(v uint64) {
		h = (h ^ v) * prime64
	}
	mixStr := func(s string) {
		for i := 0; i < len(s); i++ {
			mix(uint64(s[i]))
		}
		mix(0xff)
	}
	mix(uint64(m.width))
	if m.toolsExpanded {
		mix(1)
	}
	if len(m.overrides) > 0 {
		ids := make([]string, 0, len(m.overrides))
		for id := range m.overrides {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			mixStr(id)
			mix(uint64(m.overrides[id]))
		}
	}
	mix(0xfe)
	if m.navigationMode {
		mix(1)
	}
	mix(uint64(m.selectedItem))
	mix(uint64(len(items)))
	return h
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
