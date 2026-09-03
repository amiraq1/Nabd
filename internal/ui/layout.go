package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// layoutMetrics is the single source of truth for all dimensional calculations
// in the Feed view. It is computed once by computeLayout() and consumed by
// View() without any further dimension mutations.
type layoutMetrics struct {
	TerminalWidth  int // raw terminal width after minViewportWidth clamp
	TerminalHeight int // raw terminal height after minimum clamp

	// Chrome heights (rows):
	HeaderRows        int // 0 or 1
	RuntimeStatusRows int // 0 or 1
	TopSepRows        int // 0 or 1 (separator above composer)
	ComposerRows      int // 1..8 or 1 (modal: paused line)
	BottomSepRows     int // 0 or 1 (separator below composer)
	FooterRows        int // 1 (help / shortcuts)
	UnseenRows        int // 0 or 1
	ModalRows         int // 0 or N (PermissionModal)
	MenuRows          int // 0 or N (SlashMenu)

	// Derived:
	ViewportRows int // max(0, TerminalHeight - sum(chrome))

	// Rendered blocks (set by View after layout, not mutations):
	headerLine        string
	runtimeStatusLine string
	footerLine        string
	topSep            string
	bottomSep         string
	pausedLine        string
}

// computeLayout calculates the layout for the current Feed state.
// It does NOT mutate the composer or viewport; it only reads dimensions.
func (m *Feed) computeLayout() layoutMetrics {
	lm := layoutMetrics{
		TerminalWidth:  m.width,
		TerminalHeight: m.height,
	}
	if lm.TerminalWidth < minViewportWidth {
		lm.TerminalWidth = minViewportWidth
	}
	if lm.TerminalHeight < 1 {
		lm.TerminalHeight = 1
	}

	w := lm.TerminalWidth

	// Header row.
	if m.header != "" {
		lm.HeaderRows = 1
		lm.headerLine = truncateToWidth(m.header, w, "…")
	}

	// Runtime status (above top separator) — 1 row or 0.
	rtText := m.runtimeStatusText()
	if rtText != "" {
		lm.RuntimeStatusRows = 1
		lm.runtimeStatusLine = truncateToWidth("· "+rtText, w, "…")
	}

	// Separators — always present when width is sufficient.
	lm.TopSepRows = 1
	lm.BottomSepRows = 1
	lm.topSep = separatorLine(w)
	lm.bottomSep = separatorLine(w)

	// Composer rows (modal: 1 row placeholder; normal: actual height).
	if m.modalVisible || m.decisionPending {
		lm.ComposerRows = 1
		pausedMsg := "· permission decision required — composer paused"
		if ansi.StringWidth(pausedMsg) > w {
			pausedMsg = "· permission required — composer paused"
		}
		if ansi.StringWidth(pausedMsg) > w {
			pausedMsg = ansi.Truncate("· composer paused", w, "…")
		}
		lm.pausedLine = pausedMsg
	} else {
		// Actual composer height after content-driven resize.
		m.composer.resize(w, maxComposerHeight)
		lm.ComposerRows = m.composer.height
	}

	// Footer row (help shortcuts).
	lm.FooterRows = 1
	lm.footerLine = m.footerText(w)

	// Unseen indicator.
	if m.unseen > 0 && !m.follow {
		lm.UnseenRows = 1
	}

	// Modal rows (below viewport in view).
	if m.modalVisible {
		lm.ModalRows = m.permModal.lineCount()
	}

	// Menu rows (above composer).
	if m.menu.visible {
		lm.MenuRows = m.menu.lineCount()
	}

	// Viewport: remaining rows after chrome.
	chrome := lm.HeaderRows +
		lm.RuntimeStatusRows +
		lm.TopSepRows +
		lm.ModalRows +
		lm.MenuRows +
		lm.UnseenRows +
		lm.ComposerRows +
		lm.BottomSepRows +
		lm.FooterRows
	lm.ViewportRows = lm.TerminalHeight - chrome
	if lm.ViewportRows < 0 {
		lm.ViewportRows = 0
	}

	// Degradation: when space is very tight, sacrifice chrome in priority order.
	// (separators → runtime status → header)
	if lm.ViewportRows == 0 {
		// Try dropping bottom sep.
		if lm.BottomSepRows > 0 {
			lm.BottomSepRows = 0
			chrome--
			lm.ViewportRows = lm.TerminalHeight - chrome
		}
	}
	if lm.ViewportRows < 0 {
		lm.ViewportRows = 0
	}
	if lm.TerminalHeight-chrome < 0 {
		// Top sep.
		if lm.TopSepRows > 0 {
			lm.TopSepRows = 0
			chrome--
		}
	}
	if lm.ViewportRows < 0 {
		lm.ViewportRows = 0
	}

	return lm
}

// runtimeStatusText returns the current runtime status string (one line, no newlines).
// This drives the Runtime Status row (above top separator). Empty when idle and no error.
func (m *Feed) runtimeStatusText() string {
	if m.status != "" {
		return m.status
	}
	if m.decisionPending {
		return "Waiting for permission…"
	}
	if m.modalVisible {
		return "Permission Required"
	}
	if m.running {
		return "Generating…"
	}
	if m.busy {
		return "Working…"
	}
	return ""
}

// footerText returns the condensed help / shortcut line adapted to the available width.
// It degrades gracefully: drops model name first, then less-important shortcuts.
func (m *Feed) footerText(width int) string {
	var full, mid, short, tiny string
	if m.modalVisible || m.decisionPending {
		if m.decisionPending {
			full = "submitting decision…"
			mid = full
			short = full
			tiny = full
		} else {
			full = "y once · a session · n deny · Enter confirm · Up/Down select"
			mid = "y once · a session · n deny · Enter confirm"
			short = "y once · a/n · Enter ok"
			tiny = "y/a/n"
		}
	} else if m.running || m.busy {
		full = "Enter send · Ctrl+J newline · PgUp/PgDn scroll · Ctrl+C cancel"
		mid = "Enter send · Ctrl+J new · PgUp/PgDn · ^C cancel"
		short = "Enter send · ^C cancel"
		tiny = "Enter · ^C"
	} else {
		full = "Enter send · Ctrl+J newline · PgUp/PgDn scroll · Ctrl+C quit · Ctrl+D exit"
		mid = "Enter send · Ctrl+J new · PgUp/PgDn · ^C quit"
		short = "Enter send · ^C quit"
		tiny = "Enter · ^C"
	}

	for _, candidate := range []string{full, mid, short, tiny} {
		if ansi.StringWidth(candidate) <= width {
			return candidate
		}
	}
	// Last resort: hard truncate.
	return ansi.Truncate(tiny, width, "")
}

// View renders the full screen using a pre-computed layout. It does NOT
// mutate state. Defensive final clamp ensures output never exceeds terminal height.
func (m *Feed) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	lm := m.computeLayout()
	w := lm.TerminalWidth

	var b strings.Builder

	// 1. Header.
	if lm.HeaderRows > 0 {
		b.WriteString(lm.headerLine)
		b.WriteByte('\n')
	}

	// 2. Viewport (feed lines).
	if lm.ViewportRows > 0 && len(m.lines) > 0 {
		start := m.scrollTop
		if start >= len(m.lines) {
			start = max(0, len(m.lines)-lm.ViewportRows)
		}
		end := min(start+lm.ViewportRows, len(m.lines))
		for i := start; i < end; i++ {
			line := m.lines[i]
			// Each stored line must already satisfy width; this is an extra guard.
			if ansi.StringWidth(line) > w {
				line = ansi.Truncate(line, w, "…")
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	// 3. Unseen indicator.
	if lm.UnseenRows > 0 {
		indicator := truncateToWidth("v updates available", w, "…")
		b.WriteString(dim.Render(indicator))
		b.WriteByte('\n')
	}

	// 4. Runtime status (above top separator).
	if lm.RuntimeStatusRows > 0 {
		b.WriteString(dim.Render(lm.runtimeStatusLine))
		b.WriteByte('\n')
	}

	// 5. Modal (above separator when visible — overlay in feed area).
	if lm.ModalRows > 0 {
		b.WriteString(m.permModal.view(w))
		b.WriteByte('\n')
	}

	// 6. Slash menu (directly above composer).
	if lm.MenuRows > 0 {
		b.WriteString(m.menu.view(w))
		b.WriteByte('\n')
	}

	// 7. Top separator.
	if lm.TopSepRows > 0 {
		b.WriteString(dim.Render(lm.topSep))
		b.WriteByte('\n')
	}

	// 8. Composer slot.
	if m.modalVisible || m.decisionPending {
		b.WriteString(dim.Render(lm.pausedLine))
	} else {
		b.WriteString(m.composer.view())
	}
	b.WriteByte('\n')

	// 9. Bottom separator.
	if lm.BottomSepRows > 0 {
		b.WriteString(dim.Render(lm.bottomSep))
		b.WriteByte('\n')
	}

	// 10. Footer.
	b.WriteString(dim.Render(lm.footerLine))

	output := b.String()

	// Defensive clamp: never emit more rows than the terminal can show.
	// Trim from the top of the viewport (never from composer/footer).
	outLines := strings.Split(output, "\n")
	if len(outLines) > m.height && m.height > 0 {
		// Keep the bottom m.height lines (Composer + Footer are at bottom).
		outLines = outLines[len(outLines)-m.height:]
		return strings.Join(outLines, "\n")
	}
	return output
}
