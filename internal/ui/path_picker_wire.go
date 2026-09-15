package ui

// Feed-side wiring for the @ path picker. The popup itself (token detection,
// ranking, completion, rendering) is in path_picker.go and has no knowledge
// of the model; everything that touches Feed state is here.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SetPickerRoot tells the picker which directory to index. The CLI wires it
// to the session root. Changing it invalidates the cached index rather than
// silently completing against the previous tree.
func (m *Feed) SetPickerRoot(dir string) {
	if m.pickerRoot == dir {
		return
	}
	m.pickerRoot = dir
	m.pickerIndex = pathIndex{}
	m.pickerScanned = false
	m.pickerErr = ""
}

// ensurePathIndex scans the root at most once, on the first @ that needs it.
//
// Scanning lazily rather than at startup is deliberate: the walk is bounded
// but not free (measured in #110-#112), and a session that never types @
// should never pay for it. Scanning once rather than per keystroke is the
// other half of that: ranking is pure string work, the walk is I/O.
//
// A stale index is the accepted cost. Files created after the first @ will
// not appear until the next session, which is why the picker never blocks a
// path: it completes text the user can still edit by hand.
func (m *Feed) ensurePathIndex() bool {
	if m.pickerScanned {
		return m.pickerErr == ""
	}
	m.pickerScanned = true
	root := m.pickerRoot
	if root == "" {
		// gitDir is the only root the model already knows. Falling back to
		// it keeps @ working in the normal case (a repo) without inventing
		// a root the session never approved.
		root = m.gitDir
	}
	if root == "" {
		m.pickerErr = "no directory to index for @"
		return false
	}
	idx, err := scanPathIndex(root)
	if err != nil {
		// Say why instead of showing an empty popup, which would read as
		// "this repository has no files".
		m.pickerErr = "cannot index files for @: " + errSummary(err)
		return false
	}
	m.pickerIndex = idx
	return true
}

// shouldOpenPathPicker holds the preconditions for the popup:
//
//  1. no permission decision is on screen — the modal owns the keyboard;
//  2. the composer is focused;
//  3. the slash menu is not open — two popups must never compete for Tab;
//  4. the text is not a slash command, where a path means nothing;
//  5. the text ends in an open @ reference.
func (m *Feed) shouldOpenPathPicker() bool {
	if m.modalVisible || m.decisionPending {
		return false
	}
	if !m.composer.focused() {
		return false
	}
	if m.menu.visible {
		return false
	}
	text := m.composer.value()
	if strings.HasPrefix(text, "/") {
		return false
	}
	_, ok := findAtToken(text)
	return ok
}

// syncPathPicker recomputes the popup from the composer text. Called after
// every edit, exactly like syncSlashMenu.
func (m *Feed) syncPathPicker() {
	if !m.shouldOpenPathPicker() {
		m.picker.close()
		return
	}
	tok, ok := findAtToken(m.composer.value())
	if !ok {
		m.picker.close()
		return
	}
	if !m.ensurePathIndex() {
		m.picker.close()
		if m.pickerErr != "" {
			m.setStatus(m.pickerErr, rankResult)
		}
		return
	}
	items := matchPaths(m.pickerIndex.paths, tok.query, pickerMaxItems)
	if len(items) == 0 {
		// No match is not an error: the popup closes and the typed text
		// stays exactly as written.
		m.picker.close()
		return
	}
	m.picker.open(items, tok, !m.pickerIndex.complete)
}

// pickerKey handles keys while the popup is open. It mirrors menuKey so the
// two popups behave identically: Up/Down move, Tab/Enter complete, Esc
// closes, anything else is an edit.
//
// Enter completes and never sends. The alternative — Enter sends, Tab
// completes — would make a visible highlighted row do nothing on the most
// obvious key, and would send a half-typed path on a keystroke the user
// aimed at the popup.
func (m *Feed) pickerKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case k.Type == tea.KeyUp:
		m.picker.prev()
		return m, nil
	case k.Type == tea.KeyDown:
		m.picker.next()
		return m, nil
	case k.Type == tea.KeyTab, k.Type == tea.KeyEnter:
		if k.Paste {
			return m.composerEdit(k)
		}
		return m.completePickerSelection()
	case k.Type == tea.KeyEsc:
		// Esc closes the popup only. Browse mode is one Esc further, so a
		// user dismissing the popup does not also lose composer focus.
		m.picker.close()
		return m, nil
	case isNewlineKey(k):
		return m.insertNewline()
	default:
		return m.composerEdit(k)
	}
}

// completePickerSelection writes the highlighted path into the composer.
// The input limit is enforced before the write, never by cutting the result.
func (m *Feed) completePickerSelection() (tea.Model, tea.Cmd) {
	p, ok := m.picker.currentPath()
	if !ok {
		m.picker.close()
		return m, nil
	}
	completed := completeAtToken(m.composer.value(), m.picker.token, p)
	if inputTooLong(completed) && !inputTooLong(m.composer.value()) {
		m.setStatus(limitNotice, rankResult)
		m.picker.close()
		return m, nil
	}
	m.composer.setValue(completed)
	m.history.edited()
	m.history.setDraft(completed)
	m.composer.growToContent(maxComposerHeight)
	m.picker.close()
	return m, nil
}

// pickerVisible reports whether the popup is on screen (tests, layout).
func (m *Feed) pickerVisible() bool { return m.picker != nil && m.picker.visible }
