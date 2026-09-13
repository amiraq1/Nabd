package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// searchMatch represents a single search hit in the projected feed.
type searchMatch struct {
	itemIndex int // card index in m.navigationItems()
	lineIndex int // line index in m.lines
	runeStart int // rune offset within the line where match starts
	runeEnd   int // rune offset within the line where match ends
}

// searchState tracks interactive feed search across projected card lines.
type searchState struct {
	active     bool
	query      string
	matches    []searchMatch
	matchIndex int

	prevScrollTop int
	prevSelected  int
}

// enterSearch activates search mode, capturing initial scroll and selection state.
// It is strictly blocked while a permission modal is visible or pending.
func (m *Feed) enterSearch() (tea.Model, tea.Cmd) {
	if m.modalVisible || m.decisionPending {
		return m, nil
	}
	m.search = searchState{
		active:        true,
		query:         "",
		matches:       nil,
		matchIndex:    -1,
		prevScrollTop: m.scrollTop,
		prevSelected:  m.selectedItem,
	}
	m.setStatus("/", rankResult)
	return m, nil
}

// cancelSearch dismisses search mode and restores the previous viewport position.
func (m *Feed) cancelSearch() (tea.Model, tea.Cmd) {
	m.search.active = false
	m.search.query = ""
	m.search.matches = nil
	m.search.matchIndex = -1
	m.scrollTop = m.search.prevScrollTop
	m.selectItemInPlace(m.search.prevSelected)
	m.clearStatus()
	m.clampScroll()
	m.refresh()
	return m, nil
}

// updateSearchMatches searches m.lines for occurrences of m.search.query using
// rune-safe offsets and case-insensitive comparison. It never mutates the
// projector or journal.
func (m *Feed) updateSearchMatches() {
	if m.search.query == "" {
		m.search.matches = nil
		m.search.matchIndex = -1
		m.setStatus("/", rankResult)
		return
	}

	qLower := strings.ToLower(m.search.query)
	qRunes := []rune(m.search.query)
	qRuneLen := len(qRunes)
	if qRuneLen == 0 {
		m.search.matches = nil
		m.search.matchIndex = -1
		m.setStatus("/", rankResult)
		return
	}

	var matches []searchMatch
	for lineIdx, line := range m.lines {
		// Clean off only the exact gutter marker emitted by the feed.
		clean := stripCardGutter(line)
		offset := 0
		if len(clean) != len(line) {
			offset = 2
		}

		lineLower := strings.ToLower(clean)
		if !strings.Contains(lineLower, qLower) {
			continue
		}

		itemIdx := m.itemAt(lineIdx)
		runes := []rune(clean)
		rLen := len(runes)

		for i := 0; i <= rLen-qRuneLen; i++ {
			sub := string(runes[i : i+qRuneLen])
			if strings.ToLower(sub) == qLower {
				matches = append(matches, searchMatch{
					itemIndex: itemIdx,
					lineIndex: lineIdx,
					runeStart: offset + i,
					runeEnd:   offset + i + qRuneLen,
				})
			}
		}
	}

	m.search.matches = matches
	if len(matches) == 0 {
		m.search.matchIndex = -1
		m.setStatus(fmt.Sprintf("[0/0] /%s", m.search.query), rankResult)
		return
	}

	m.search.matchIndex = 0
	m.jumpToMatch(0)
}

// jumpToMatch centers the viewport on the match at idx and selects the containing card.
func (m *Feed) jumpToMatch(idx int) {
	if idx < 0 || idx >= len(m.search.matches) {
		return
	}
	match := m.search.matches[idx]
	if match.itemIndex >= 0 {
		m.selectItemInPlace(match.itemIndex)
	}

	lm := m.computeLayout()
	vh := lm.ViewportRows
	if vh > 0 {
		if match.lineIndex < m.scrollTop || match.lineIndex >= m.scrollTop+vh {
			m.scrollTop = max(0, match.lineIndex-vh/2)
			m.clampScroll()
			m.refresh()
		}
	}

	m.setStatus(fmt.Sprintf("[%d/%d] /%s", idx+1, len(m.search.matches), m.search.query), rankResult)
}

// searchNextMatch advances to the next search hit, wrapping to the start.
func (m *Feed) searchNextMatch() {
	if len(m.search.matches) == 0 {
		return
	}
	m.search.matchIndex = (m.search.matchIndex + 1) % len(m.search.matches)
	m.jumpToMatch(m.search.matchIndex)
}

// searchPrevMatch retreats to the previous search hit, wrapping to the end.
func (m *Feed) searchPrevMatch() {
	if len(m.search.matches) == 0 {
		return
	}
	m.search.matchIndex = (m.search.matchIndex - 1 + len(m.search.matches)) % len(m.search.matches)
	m.jumpToMatch(m.search.matchIndex)
}

// isSearchPrevMatchKey reports whether the key indicates retreating to previous match.
func isSearchPrevMatchKey(k tea.KeyMsg) bool {
	if k.Type == tea.KeyUp || k.Type == tea.KeyShiftTab || k.Type == tea.KeyCtrlP {
		return true
	}
	return k.String() == "shift+enter" || (k.Type == tea.KeyEnter && k.Alt)
}

// searchKey routes keyboard input while search mode is active.
func (m *Feed) searchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if isSearchPrevMatchKey(k) {
		m.searchPrevMatch()
		return m, nil
	}
	switch k.Type {
	case tea.KeyEsc:
		return m.cancelSearch()
	case tea.KeyEnter:
		m.searchNextMatch()
		return m, nil
	case tea.KeyDown:
		m.searchNextMatch()
		return m, nil
	case tea.KeyBackspace:
		if len(m.search.query) > 0 {
			r := []rune(m.search.query)
			m.search.query = string(r[:len(r)-1])
			m.updateSearchMatches()
		}
		return m, nil
	case tea.KeyRunes:
		m.search.query += string(k.Runes)
		m.updateSearchMatches()
		return m, nil
	default:
		return m, nil
	}
}
