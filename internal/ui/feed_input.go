package ui

import (
	"context"
	"strings"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// This file owns the deterministic input router and every key, mouse, and
// send handler. Pure moves out of feed.go: no behaviour changes.

// routeKey is the deterministic input router. Precedence:
//
//  1. Ctrl-C / Ctrl-D (safety keys, always first)
//  2. Permission modal
//  3. Composer (when focused)
//  4. Viewport scrolling
//  5. Global shortcuts
func (m *Feed) routeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyCtrlC:
		return m.onCtrlC()
	case tea.KeyCtrlD:
		return m.onCtrlD()
	}

	if m.modalVisible || m.decisionPending {
		return m.modalKey(k)
	}
	if m.menu.visible {
		return m.menuKey(k)
	}
	if isToggleToolsKey(k) {
		return m.toggleTools()
	}
	if m.composer.focused() {
		return m.composerKey(k)
	}
	return m.viewportKey(k)
}

// toggleTools toggles between compact and expanded tool output.
// Follow mode keeps view anchored to the bottom.
// Browsing history preserves the visible content anchor.
func (m *Feed) toggleTools() (tea.Model, tea.Cmd) {
	items := mergeNotices(m.proj.Items(), m.notices)
	if len(items) > maxVisibleFeedItems {
		items = items[len(items)-maxVisibleFeedItems:]
	}

	if m.follow {
		m.toolsExpanded = !m.toolsExpanded
		m.refresh()
		m.scrollToEnd()
		return m, nil
	}

	// Follow is false: preserve visible content anchor.
	_, oldOffsets := renderItemsWithOffsets(items, m.width, m.toolsExpanded)

	// Find which item currently anchors scrollTop.
	anchorIdx := 0
	offsetWithin := 0
	for i := len(oldOffsets) - 1; i >= 0; i-- {
		if m.scrollTop >= oldOffsets[i] {
			anchorIdx = i
			offsetWithin = m.scrollTop - oldOffsets[i]
			break
		}
	}

	m.toolsExpanded = !m.toolsExpanded
	newLines, newOffsets := renderItemsWithOffsets(items, m.width, m.toolsExpanded)
	m.lines = newLines

	if anchorIdx < len(newOffsets) {
		targetTop := newOffsets[anchorIdx] + offsetWithin
		nextItemStart := len(newLines)
		if anchorIdx+1 < len(newOffsets) {
			nextItemStart = newOffsets[anchorIdx+1]
		}
		if targetTop >= nextItemStart {
			targetTop = newOffsets[anchorIdx]
		}
		m.scrollTop = targetTop
	}
	m.clampScroll()
	return m, nil
}

// onCtrlC implements the deterministic cancel policy:
//   - Modal visible: cancel the in-flight run; never approve, never quit.
//   - Run in flight: cancel it; never quit mid-flight.
//   - Composer non-empty: clear it (and history browsing); never quit.
//   - Otherwise: quit (the app's exit policy, unchanged).
//
// Cancellation calls m.cancel() (a context.CancelFunc) directly: the run
// command may be blocking the Bubble Tea loop right now, so the cancel must
// not travel through another tea.Msg — that message could not be processed
// until the blocked run returns. context.CancelFunc is safe to call from
// any goroutine and never touches the Bubble Tea model.
func (m *Feed) onCtrlC() (tea.Model, tea.Cmd) {
	if m.modalVisible || m.decisionPending {
		if m.running || m.busy {
			m.cancelRun("canceling…")
		}
		// A modal with no run behind it (orphan ask): ignore safely.
		return m, nil
	}
	if m.running || m.busy {
		m.cancelRun("canceling…")
		return m, nil
	}
	if !m.composer.isEmpty() {
		m.composer.clear()
		m.history.resetBrowsing()
		m.menu.close()
		m.status = ""
		return m, nil
	}
	// Empty composer, idle: quit.
	return m, tea.Quit
}

// onCtrlD implements the deterministic exit policy:
//   - Modal visible: the modal owns it; ignore safely (never approve,
//     never quit, never reach the composer or exit handler).
//   - Composer non-empty: delete the rune under the cursor (rune-safe),
//     never quit.
//   - Composer empty: quit only when every safe-exit condition holds.
func (m *Feed) onCtrlD() (tea.Model, tea.Cmd) {
	if m.modalVisible || m.decisionPending {
		return m, nil
	}
	if !m.composer.isEmpty() {
		cmd := m.composer.deleteForward()
		m.syncSlashMenu()
		return m, cmd
	}
	if m.safeToQuit() {
		return m, tea.Quit
	}
	m.status = "cannot exit now: run in progress or state not clean"
	return m, nil
}

// safeToQuit reports whether every exit precondition holds.
func (m *Feed) safeToQuit() bool {
	if m.running || m.busy || m.cancel != nil {
		return false
	}
	if m.modalVisible || m.decisionPending {
		return false
	}
	return true
}

// cancelRun cancels the in-flight run context directly (never via a
// message, see onCtrlC). Repeated cancellation is a no-op.
func (m *Feed) cancelRun(status string) {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.runningTool = ""
	if status != "" {
		m.status = status
	}
}

// modalKey routes keys while the permission modal is visible. Every
// ordinary key goes to the modal; the composer, viewport and history are
// untouched, and nothing can be sent.
func (m *Feed) modalKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.decisionPending {
		return m, nil
	}

	switch k.Type {
	case tea.KeyUp, tea.KeyLeft:
		m.permModal.prevChoice()
		return m, nil
	case tea.KeyDown, tea.KeyRight:
		m.permModal.nextChoice()
		return m, nil
	case tea.KeyEnter:
		if m.permModal.selected >= 0 {
			return m.answerModal(m.permModal.currentDecision())
		}
		return m, nil
	case tea.KeyEsc:
		return m.answerModal(agent.Deny)
	case tea.KeyCtrlC:
		// Cancels the in-flight run; never approves.
		if m.running || m.busy {
			m.cancelRun("canceling…")
		}
		return m, nil
	}

	switch k.String() {
	case "y", "Y":
		return m.answerModal(agent.AllowOnce)
	case "a", "A":
		return m.answerModal(agent.AllowSession)
	case "n", "N":
		return m.answerModal(agent.Deny)
	case "esc":
		return m.answerModal(agent.Deny)
	default:
		// Swallowed by the modal.
		return m, nil
	}
}

// answerModal forwards a permission decision to the approver and restores
// composer focus. The loop answers with a PermReply event (which also
// clears any residual modal state); the focus restore happens here so the
// keyboard is usable immediately even before that event arrives.
func (m *Feed) answerModal(d agent.Decision) (tea.Model, tea.Cmd) {
	if m.decisionPending {
		return m, nil
	}
	m.decisionPending = true
	m.permModal.decisionPending = true
	m.modalVisible = false
	m.pending = nil
	if !m.composer.focused() {
		m.composer.focus()
	}
	return m, func() tea.Msg { return permReplyMsg{Decision: d} }
}

// composerKey routes keys while the composer is focused.
func (m *Feed) composerKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case isSendKey(k):
		return m.trySend()
	case isNewlineKey(k):
		// Alt+Enter / Ctrl+J insert a newline instead of sending. The
		// textarea only binds plain Enter to InsertNewline, so both are
		// converted to a plain Enter for the textarea.
		return m.insertNewline()
	case k.Type == tea.KeyUp:
		return m.composerUp()
	case k.Type == tea.KeyDown:
		return m.composerDown()
	case k.Type == tea.KeyPgUp || k.Type == tea.KeyPgDown:
		// Explicit scroll keys go to the viewport even while the composer
		// is focused (they are not editing keys).
		return m.viewportKey(k)
	default:
		// Editing keys and runes: pass through, then enforce the limits.
		return m.composerEdit(k)
	}
}

// isSendKey reports whether the key means "send the message": a plain
// Enter, never Alt+Enter, never a bracketed-paste rune.
func isSendKey(k tea.KeyMsg) bool {
	if k.Paste {
		return false
	}
	return k.Type == tea.KeyEnter && !k.Alt
}

// isNewlineKey reports whether the key is a documented newline shortcut:
// Ctrl+J (primary, reliable on Termux) or Alt+Enter when it arrives as a
// distinct key. Shift+Enter is never relied upon.
func isNewlineKey(k tea.KeyMsg) bool {
	if k.Paste {
		return false
	}
	if k.Type == tea.KeyCtrlJ {
		return true
	}
	return k.Type == tea.KeyEnter && k.Alt
}

// isToggleToolsKey reports whether the key is the tool expansion toggle:
// Ctrl+O. It explicitly guards against bracketed paste content.
func isToggleToolsKey(k tea.KeyMsg) bool {
	if k.Paste {
		return false
	}
	return k.Type == tea.KeyCtrlO || (k.Type == tea.KeyRunes && len(k.Runes) == 1 && k.Runes[0] == 0x0f)
}

// trySend implements the send policy:
//   - a slash command (/undo, /compact, /ctx, /edits, /rewind, /help) is
//     handled locally via the CLI callbacks, never sent to the model;
//   - empty/whitespace-only text: never send;
//   - no runner available: reject, keep the text, no history entry;
//   - a run already in flight: reject (no queue), keep the text, do not
//     touch history;
//   - text over the limits: reject with a notice;
//   - otherwise: accept — clear the composer, reset history browsing, add
//     the message to history, and start the run.
func (m *Feed) trySend() (tea.Model, tea.Cmd) {
	m.menu.close()
	text := m.composer.value()
	if strings.TrimSpace(text) == "" {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		if m.busy {
			m.status = "wait for the current run to finish first"
			return m, nil
		}
		return m.runCommand(text)
	}
	if m.runner == nil {
		// Nothing can ever accept this message: keep the text, show why.
		m.status = "error: no runner available"
		return m, nil
	}
	if m.busy {
		m.status = "a run is in progress; cancel it or wait before sending"
		return m, nil
	}
	if inputTooLong(text) {
		m.status = limitNotice
		return m, nil
	}
	// Accept the send.
	m.composer.clear()
	m.history.resetBrowsing()
	m.history.add(text)
	m.running = true
	m.busy = true
	m.errorSeenSinceSend = false
	m.status = ""
	return m, m.startRun(text)
}

// runCommand handles a slash command locally. The composer is cleared on
// success; the returned text becomes the new composer value (rewind
// restores the cut message for editing). Unknown commands keep the text in
// the composer and show an error.
func (m *Feed) runCommand(line string) (tea.Model, tea.Cmd) {
	parsed := ParseSlashCommand(line)
	if !parsed.Valid {
		m.status = parsed.Error
		return m, nil
	}
	switch parsed.Command.Name {
	case "/undo":
		m.composer.clear()
		if m.callbacks.OnUndo == nil {
			m.status = "undo not supported in this version"
			return m, nil
		}
		m.status = m.callbacks.OnUndo(parsed.N)
		return m, nil
	case "/rewind":
		if m.callbacks.OnRewind == nil {
			m.status = "rewind not supported in this version"
			return m, nil
		}
		restored, status := m.callbacks.OnRewind(parsed.N)
		m.composer.clear()
		m.composer.setValue(restored)
		m.history.resetBrowsing()
		m.status = status
		if status == "" {
			m.status = "rewound"
		}
		return m, nil
	case "/ctx":
		m.composer.clear()
		if m.callbacks.OnCtx == nil {
			m.status = "—"
			return m, nil
		}
		m.status = m.callbacks.OnCtx()
		return m, nil
	case "/compact":
		m.composer.clear()
		if m.callbacks.OnCompact == nil {
			m.status = "—"
			return m, nil
		}
		m.status = m.callbacks.OnCompact()
		return m, nil
	case "/edits":
		m.composer.clear()
		if m.callbacks.OnEdits == nil {
			m.status = "—"
			return m, nil
		}
		m.status = m.callbacks.OnEdits()
		return m, nil
	case "/help":
		m.composer.clear()
		m.status = "/undo [n] · /edits · /ctx · /compact · /rewind [n]"
		return m, nil
	}
	// Unknown command: keep the text, tell the user.
	m.status = "unknown command: " + parsed.RawCmd
	return m, nil
}

// startRun launches the accepted message on the runner. The caller (trySend)
// has already verified the runner exists and the text is within limits, so
// a run always starts here.
func (m *Feed) startRun(text string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return func() tea.Msg {
		err := m.runner.Run(ctx, text)
		cancel()
		return doneMsg{err}
	}
}

// insertNewline inserts a single newline at the cursor, enforcing the line
// limit atomically. The textarea's Enter binding does the split.
func (m *Feed) insertNewline() (tea.Model, tea.Cmd) {
	text := m.composer.value()
	if countInputLines(text)+1 > maxInputLines {
		m.status = limitNotice
		return m, nil
	}
	// Enter as a rune insert is handled by giving the textarea its own
	// InsertNewline key (plain Enter). Alt is stripped because the textarea
	// only matches on the plain key.
	cmd := m.composer.update(tea.KeyMsg{Type: tea.KeyEnter})
	m.syncSlashMenu()
	return m, cmd
}

// composerUp applies the history rule: Up recalls the previous history
// entry only when the composer is empty or the cursor is on the first
// logical line. Otherwise it is a normal cursor move inside the textarea.
func (m *Feed) composerUp() (tea.Model, tea.Cmd) {
	text := m.composer.value()
	onFirst := text == "" || m.composer.cursorLogicalLine() == 0
	if !onFirst {
		return m.composerMove(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.history.len() == 0 {
		if text == "" {
			return m, nil
		}
		return m.composerMove(tea.KeyMsg{Type: tea.KeyUp})
	}
	// Save the draft on the first transition into browsing.
	if !m.history.browsing() {
		m.history.saveDraft(text)
	}
	s, ok := m.history.up()
	if !ok {
		return m, nil // already at the oldest entry; text unchanged
	}
	m.composer.setValue(s)
	return m, nil
}

// composerDown applies the history rule: Down recalls the newer entry only
// while browsing and when the cursor is on the last logical line of the
// recalled text. Past the newest entry the saved draft is restored and
// browsing ends.
func (m *Feed) composerDown() (tea.Model, tea.Cmd) {
	if !m.history.browsing() {
		return m.composerMove(tea.KeyMsg{Type: tea.KeyDown})
	}
	text := m.composer.value()
	onLast := text == "" || m.composer.cursorLogicalLine() >= m.composer.logicalLineCount()-1
	if !onLast {
		return m.composerMove(tea.KeyMsg{Type: tea.KeyDown})
	}
	s, ok := m.history.down()
	if !ok {
		return m, nil
	}
	m.composer.setValue(s)
	return m, nil
}

// composerMove hands a navigation key to the textarea (a plain cursor
// move, not history).
func (m *Feed) composerMove(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.composerEdit(k)
}

// composerEdit passes an editing key to the textarea, then enforces the
// input limits atomically: if the mutation crossed a limit the whole
// change is rolled back and a notice is shown (never a silent cut).
// Historical content that already exceeded the cap (e.g. recalled from an
// old journal before this policy) may still be edited down; only growth
// past the cap is blocked.
func (m *Feed) composerEdit(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	before := m.composer.value()
	cmd := m.composer.update(k)
	after := m.composer.value()
	if after != before {
		alreadyOver := inputTooLong(before)
		if inputTooLong(after) && !alreadyOver {
			m.composer.setValue(before)
			m.status = limitNotice
			return m, nil
		}
		// Any real edit ends history browsing; the edited text becomes the
		// new draft.
		m.history.edited()
		m.history.setDraft(after)
		m.status = ""
		m.composer.growToContent(maxComposerHeight)
	}
	m.syncSlashMenu()
	return m, cmd
}

// menuKey handles keyboard interaction while the slash command menu is open.
func (m *Feed) menuKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case k.Type == tea.KeyUp:
		m.menu.prev()
		return m, nil
	case k.Type == tea.KeyDown:
		m.menu.next()
		return m, nil
	case k.Type == tea.KeyTab, k.Type == tea.KeyEnter:
		// Complete the selected command into composer text. NEVER executes simultaneously!
		if cmd, ok := m.menu.currentCommand(); ok {
			completed := cmd.Name + " "
			m.composer.setValue(completed)
			m.history.setDraft(completed)
			m.composer.growToContent(maxComposerHeight)
		}
		m.menu.close()
		return m, nil
	case k.Type == tea.KeyEsc:
		m.menu.close()
		return m, nil
	case isNewlineKey(k):
		return m.insertNewline()
	default:
		// Ordinary typing or edits: delegate to composerEdit, which syncs the menu.
		return m.composerEdit(k)
	}
}

// shouldOpenSlashMenu checks preconditions for opening the slash command menu:
//  1. Permission modal is not visible or pending.
//  2. Composer is focused.
//  3. Input starts with '/' (no leading whitespace).
//  4. Single line input, cursor on line 0.
//  5. Inside command token (no spaces in input yet).
func (m *Feed) shouldOpenSlashMenu() bool {
	if m.busy || m.running || m.modalVisible || m.decisionPending {
		return false
	}
	if !m.composer.focused() {
		return false
	}
	text := m.composer.value()
	if !strings.HasPrefix(text, "/") {
		return false
	}
	if strings.Contains(text, "\n") {
		return false
	}
	if strings.Contains(text, " ") {
		return false
	}
	if m.composer.cursorLogicalLine() != 0 {
		return false
	}
	return true
}

// syncSlashMenu updates the slash menu popup state based on the current composer text.
func (m *Feed) syncSlashMenu() {
	if !m.shouldOpenSlashMenu() {
		m.menu.close()
		return
	}
	text := m.composer.value()
	matches := FilterSlashCommands(text)
	if len(matches) > 0 {
		m.menu.open(matches)
	} else {
		m.menu.close()
	}
}

// viewportKey handles scrolling and global keys when the composer does not
// own the key.
func (m *Feed) viewportKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	lm := m.computeLayout()
	vh := lm.ViewportRows
	bs := m.bottomStart(vh)
	switch k.Type {
	case tea.KeyPgUp:
		m.follow = false
		m.scrollTop = max(0, m.scrollTop-vh)
		return m, nil
	case tea.KeyPgDown:
		m.scrollTop = min(bs, m.scrollTop+vh)
		if m.scrollTop == bs {
			m.follow = true
			m.unseen = 0
		}
		return m, nil
	case tea.KeyUp:
		m.follow = false
		m.scrollTop = max(0, m.scrollTop-1)
		return m, nil
	case tea.KeyDown:
		m.scrollTop = min(bs, m.scrollTop+1)
		if m.scrollTop == bs {
			m.follow = true
			m.unseen = 0
		}
		return m, nil
	case tea.KeyHome:
		m.follow = false
		m.scrollTop = 0
		return m, nil
	case tea.KeyEnd:
		m.follow = true
		m.unseen = 0
		m.scrollTop = bs
		return m, nil
	default:
		return m, nil
	}
}

// handleMouse processes mouse and touch events for the Feed viewport.
// It handles finger-swipe scrolling via vertical wheel reports, scrolling by 3 rows.
// It enforces strict viewport hit-testing and ignores gestures over chrome or during modal interaction.
func (m *Feed) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.touchEnabled {
		return m, nil
	}
	// Ignore gestures while permission interaction is active.
	if m.modalVisible || m.decisionPending {
		return m, nil
	}

	isWheelUp := msg.Button == tea.MouseButtonWheelUp
	isWheelDown := msg.Button == tea.MouseButtonWheelDown
	if !isWheelUp && !isWheelDown {
		return m, nil
	}

	lm := m.computeLayout()
	if lm.ViewportRows <= 0 {
		return m, nil
	}

	// Hit-test: coordinates must fall strictly within the conversation viewport.
	vpTop := lm.HeaderRows
	vpBottom := lm.HeaderRows + lm.ViewportRows
	if msg.Y < vpTop || msg.Y >= vpBottom || msg.X < 0 || msg.X >= lm.TerminalWidth {
		return m, nil
	}

	const touchScrollStep = 3
	bs := m.bottomStart(lm.ViewportRows)

	if isWheelUp {
		m.follow = false
		m.scrollTop = max(0, m.scrollTop-touchScrollStep)
		return m, nil
	}

	if isWheelDown {
		m.scrollTop = min(bs, m.scrollTop+touchScrollStep)
		if m.scrollTop == bs {
			m.follow = true
			m.unseen = 0
		}
		return m, nil
	}

	return m, nil
}
