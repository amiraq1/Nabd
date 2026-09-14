package ui

import (
	"context"
	"strings"
	"time"

	"nabd/internal/agent"
	"nabd/internal/presentation"

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
	if m.navigationMode {
		if model, cmd, handled := m.navigationKey(k); handled {
			return model, cmd
		}
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
		m.overrides = nil
		m.refresh()
		m.scrollToEnd()
		return m, nil
	}

	// Follow is false: preserve visible content anchor.
	// Use stored offsets: they are already in m.lines coordinate space.
	oldOffsets := m.offsets
	if len(oldOffsets) != len(items) {
		_, oldOffsets = renderItemsCached(m, items, m.width, m.toolsExpanded)
	}

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
	m.overrides = nil
	// Same bounded, cached path as refresh, so lines and offsets cannot
	// drift into different coordinate spaces.
	newLines, newOffsets := renderItemsCached(m, items, m.width, m.toolsExpanded)
	m.lines, m.offsets = newLines, newOffsets
	m.syncRenderSig()

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
		m.clearStatus()
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
	m.setStatus("cannot exit now: run in progress or state not clean", rankResult)
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
		m.setStatus(status, rankRunLifecycle)
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
		if m.permModal.call != nil && m.permModal.call.SessionGrantKnown && !m.permModal.call.SessionGrantAllowed {
			return m, nil
		}
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
	if k.Type == tea.KeyEsc {
		// Esc enters browse mode unconditionally: enterNavigation only
		// blurs the composer, so any draft stays in the buffer and Esc
		// from navigation mode brings it back. The footer advertises
		// "Esc browse" at every width, so the key must work at every width.
		m.enterNavigation()
		return m, nil
	}
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
			m.setStatus("wait for the current run to finish first", rankResult)
			return m, nil
		}
		return m.runCommand(text)
	}
	if m.runner == nil {
		// Nothing can ever accept this message: keep the text, show why.
		m.setStatus("error: no runner available", rankResult)
		return m, nil
	}
	if m.busy {
		m.setStatus("a run is in progress; cancel it or wait before sending", rankResult)
		return m, nil
	}
	if inputTooLong(text) {
		m.setStatus(limitNotice, rankResult)
		return m, nil
	}
	// Accept the send.
	m.composer.clear()
	m.history.resetBrowsing()
	m.history.add(text)
	m.running = true
	m.busy = true
	m.errorSeenSinceSend = false
	m.reqStartedAt = time.Now()
	m.firstDeltaAt = time.Time{}
	m.lastDeltaAt = time.Time{}
	m.streamedChars = 0
	m.lastThroughputAt = time.Time{}
	m.cachedLiveRate = ""
	m.cachedLiveTok = ""
	m.clearStatus()
	return m, m.startRun(text)
}

// runCommand handles a slash command locally. The composer is cleared on
// success; the returned text becomes the new composer value (rewind
// restores the cut message for editing). Unknown commands keep the text in
// the composer and show an error.
func (m *Feed) runCommand(line string) (tea.Model, tea.Cmd) {
	parsed := ParseSlashCommand(line)
	if !parsed.Valid {
		m.setStatus(parsed.Error, rankResult)
		return m, nil
	}
	switch parsed.Command.Name {
	case "/undo":
		m.composer.clear()
		if m.callbacks.OnUndo == nil {
			m.setStatus("undo not supported in this version", rankResult)
			return m, nil
		}
		m.setStatus(m.callbacks.OnUndo(parsed.N), rankResult)
		return m, nil
	case "/rewind":
		if m.callbacks.OnRewind == nil {
			m.setStatus("rewind not supported in this version", rankResult)
			return m, nil
		}
		restored, status := m.callbacks.OnRewind(parsed.N)
		m.composer.clear()
		m.composer.setValue(restored)
		m.history.resetBrowsing()
		m.setStatus(status, rankResult)
		if status == "" {
			m.setStatus("rewound", rankResult)
		}
		return m, nil
	case "/ctx":
		m.composer.clear()
		if m.callbacks.OnCtx == nil {
			m.setStatus("—", rankResult)
			return m, nil
		}
		m.setCommandResult(m.callbacks.OnCtx())
		return m, nil
	case "/compact":
		m.composer.clear()
		if m.callbacks.OnCompact == nil {
			m.setStatus("—", rankResult)
			return m, nil
		}
		m.setCommandResult(m.callbacks.OnCompact())
		return m, nil
	case "/edits":
		m.composer.clear()
		if m.callbacks.OnEdits == nil {
			m.setStatus("—", rankResult)
			return m, nil
		}
		m.setCommandResult(m.callbacks.OnEdits())
		return m, nil
	case "/help":
		m.composer.clear()
		m.setCommandResult(CommandHelp(m.width))
		return m, nil
	}
	// Unknown command: keep the text, tell the user.
	m.setStatus("unknown command: "+parsed.RawCmd, rankResult)
	return m, nil
}

// setCommandResult routes a slash command result. Single-line results go to
// the transient status line (the runtime status row). Multi-line results
// (e.g. /edits listing several pending edits) cannot live on the one-line
// status row — they are added to the feed as a permanent notice block, so
// they render through the ItemUIBlock pipeline, scroll with the feed, and
// count towards scrollTop.
func (m *Feed) setCommandResult(text string) {
	if text == "" {
		return
	}
	if strings.Contains(text, "\n") {
		m.addNotice(presentation.ItemNotice, text)
		return
	}
	m.setStatus(text, rankResult)
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
		m.setStatus(limitNotice, rankResult)
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
			m.setStatus(limitNotice, rankResult)
			return m, nil
		}
		// Any real edit ends history browsing; the edited text becomes the
		// new draft.
		m.history.edited()
		m.history.setDraft(after)
		m.clearStatus()
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
			completed := cmd.Name
			if cmd.HasArg {
				completed += " "
			}
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

// pointerLine translates a screen row into a line index within m.lines,
// or returns -1 if y falls outside the visible feed rows. It accounts
// for viewportTopPadding: when the feed is shorter than the viewport
// and following, blank rows precede the content, so a tap in that space
// is out of bounds (the pointer origin starts past the padding).
func (m *Feed) pointerLine(lm layoutMetrics, y int) int {
	top := lm.viewportTop()
	if y < top || y >= top+lm.ViewportRows {
		return -1
	}
	topPad := m.viewportTopPadding(lm)
	if y < top+topPad {
		return -1
	}
	line := m.scrollTop + (y - top - topPad)
	if line < 0 || line >= len(m.lines) {
		return -1
	}
	return line
}

// restoreScroll pins the viewport back to a saved first-visible line after a
// repaint. Every pointer gesture goes through it: content must never move
// under the finger that touched it.
func (m *Feed) restoreScroll(top int) {
	m.scrollTop = top
	m.clampScroll()
}

// handlePointerTap resolves a tap into a card and applies the deterministic
// tap policy:
//
//	tap on another card      -> select it, in place, no scrolling
//	tap on the selected card -> toggle its expansion
//
// There is no double-tap and no timer: on a phone terminal a double tap
// arrives as two unrelated press/release pairs at unpredictable intervals,
// so any threshold would be a coin flip. Tap-again-to-expand needs no
// clock and matches the keyboard, where Enter expands the selected card.
//
// A tap can never approve anything. The modal owns the pointer before this
// function is reachable (see handleMouse), and expansion only repaints
// output the projector already produced.
func (m *Feed) handlePointerTap(lm layoutMetrics, y int) (tea.Model, tea.Cmd) {
	line := m.pointerLine(lm, y)
	if line < 0 {
		return m, nil
	}
	idx := m.itemAt(line)
	if idx < 0 {
		return m, nil
	}
	// A tap is an explicit browsing intent: it stops follow, exactly like the
	// keyboard. selectItemInPlace does not touch follow, so without this the
	// next refresh would re-anchor to the bottom and drag the picked card off
	// screen.
	m.follow = false
	before := m.scrollTop
	if !m.navigationMode {
		m.navigationMode = true
		m.composer.blur()
		m.selectItemInPlace(idx)
		// Entering navigation mode flips isSelected, so the marker must be
		// repainted even when the index did not change (e.g. after Esc,
		// which keeps selectedItem). selectItemInPlace skips the repaint
		// when prev == idx, so refresh unconditionally here.
		m.refresh()
		m.restoreScroll(before)
		return m, nil
	}
	if m.selectedItem == idx {
		if m.toggleCard(idx) {
			// The card grows downward; hold scrollTop so nothing above it
			// moves. Pinning the card to the top (refreshPreservingSelection)
			// would yank the whole feed up under the finger.
			m.refresh()
			m.restoreScroll(before)
		}
		return m, nil
	}
	m.selectItemInPlace(idx)
	m.restoreScroll(before)
	return m, nil
}

// handleMouse processes mouse and touch events for the Feed viewport.
// It handles finger-swipe scrolling via vertical wheel reports, scrolling by 3 rows.
// It enforces strict viewport hit-testing and ignores gestures over chrome or during modal interaction.
func (m *Feed) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.MouseEnabled() {
		m.pointerDown = false
		m.pointerDragged = false
		return m, nil
	}
	// Ignore gestures while permission interaction is active.
	if m.modalVisible || m.decisionPending {
		m.pointerDown = false
		m.pointerDragged = false
		return m, nil
	}

	lm := m.computeLayout()
	if lm.ViewportRows <= 0 {
		m.pointerDown = false
		m.pointerDragged = false
		return m, nil
	}

	// Hit-test: coordinates must fall strictly within the conversation viewport.
	vpTop := lm.viewportTop()
	vpBottom := vpTop + lm.ViewportRows
	if msg.Y < vpTop || msg.Y >= vpBottom || msg.X < 0 || msg.X >= lm.TerminalWidth {
		m.pointerDown = false
		m.pointerDragged = false
		return m, nil
	}

	// Pointer press tracking:
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		m.pointerDown = true
		m.pointerStartX = msg.X
		m.pointerStartY = msg.Y
		m.pointerDragged = false
		return m, nil
	}

	// Pointer motion tracking:
	if msg.Action == tea.MouseActionMotion {
		if m.pointerDown {
			if msg.Y != m.pointerStartY || abs(msg.X-m.pointerStartX) > 1 {
				m.pointerDragged = true
			}
		}
		return m, nil
	}

	// Taps resolve on release, never on press or drag:
	// A press that moves into a drag is the terminal's text selection gesture.
	// It must keep working cleanly and never trigger card selection or expansion.
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease {
		wasDown := m.pointerDown
		wasDragged := m.pointerDragged
		startY := m.pointerStartY
		startX := m.pointerStartX
		m.pointerDown = false
		m.pointerDragged = false

		if !wasDown || wasDragged {
			return m, nil
		}
		if msg.Y != startY || abs(msg.X-startX) > 1 {
			return m, nil
		}
		return m.handlePointerTap(lm, msg.Y)
	}

	isWheelUp := msg.Button == tea.MouseButtonWheelUp
	isWheelDown := msg.Button == tea.MouseButtonWheelDown
	if !isWheelUp && !isWheelDown {
		return m, nil
	}
	m.pointerDown = false
	m.pointerDragged = false

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

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
