package ui

import (
	"context"
	"fmt"
	"strings"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

// Defaults for the feed viewport.
const (
	maxVisibleFeedItems = 500
	maxUIDiagnostics    = 20
	maxUINotices        = 50
	minViewportWidth    = 20
)

// Feed is the Phase 3A UI model: a projected, scrollable feed; a multiline
// composer; and a deterministic input router that arbitrates between the
// permission modal, the composer, the viewport and global shortcuts.
type Feed struct {
	proj *presentation.Projector

	// Viewport state.
	width     int
	height    int
	scrollTop int // index of the first visible rendered line
	follow    bool
	unseen    int

	// Cached rendered lines for the current viewport.
	lines []string

	// UI diagnostics (not written to journal).
	diagnostics []string

	// notices are UI-originated feed items (not in the journal): failures
	// the loop could not journal itself, e.g. the runner returning an error
	// with no RunError event. They are permanent — they live in the feed,
	// scroll with it and count towards scrollTop — unlike m.status, which
	// is transient and cleared by the next keystroke. Each notice is
	// anchored after the last journal Seq seen when it was raised so the
	// feed stays chronological.
	notices []presentation.FeedItem
	lastSeq int // highest event Seq applied so far (notice anchor)

	// errorSeenSinceSend is true once a RunError/Interrupted event arrived
	// for the current send. doneMsg uses it to avoid a duplicate notice
	// when the loop already journaled the failure.
	errorSeenSinceSend bool

	// Header info.
	header string

	// Callbacks wired by the CLI.
	callbacks FeedCallbacks

	// Composer.
	composer *composer

	// Slash command completion menu.
	menu *slashMenu

	// Permission modal state, driven by the event stream (PermAsk opens
	// it, PermReply/Interrupted close it).
	modalVisible      bool
	decisionPending   bool
	followBeforeModal bool
	permModal         *PermissionModal

	// pending holds the tool call awaiting a permission decision while the
	// modal is visible (for help text and tests).
	pending *agent.ToolCall

	// Run state.
	running     bool // an agent/model/tool run is in flight
	busy        bool // true while a run is in flight (running or awaiting permission)
	runningTool string
	cancel      context.CancelFunc

	// Runner is how a send reaches the agent loop. Set by the CLI.
	runner Runner

	// Approve answers permission requests. Set by the CLI.
	Approve *Approver

	// History of submitted user messages.
	history *userHistory

	// status is the transient status line above the composer. ASCII only,
	// like every other visible UI string.
	status string

	// prog is the live Bubble Tea program (wired by the CLI) used to
	// deliver event batches from the batcher goroutine.
	prog *tea.Program
}

// FeedCallbacks holds the hooks the feed uses to talk back to the loop.
type FeedCallbacks struct {
	OnUndo    func(n int) string
	OnCompact func() string
	// OnRewind returns the restored text (for the composer) and a status
	// message. The restored text is what /rewind cut away, put back for
	// editing.
	OnRewind func(n int) (restored, status string)
	OnCtx    func() string
	OnEdits  func() string
}

// SetHeader sets the header line shown above the viewport.
func (m *Feed) SetHeader(h string) { m.header = h }

// SetCallbacks wires the command hooks.
func (m *Feed) SetCallbacks(cb *FeedCallbacks) {
	if cb != nil {
		m.callbacks = *cb
	}
}

// SetRunner wires the agent loop runner used to start a run.
func (m *Feed) SetRunner(r Runner) { m.runner = r }

// SetApprover wires the permission answer channel.
func (m *Feed) SetApprover(a *Approver) { m.Approve = a }

// HistoryLen exposes the current history length (tests).
func (m *Feed) HistoryLen() int { return m.history.len() }

// HistoryBrowsing reports whether Up/Down history recall is active (tests).
func (m *Feed) HistoryBrowsing() bool { return m.history.browsing() }

// NewFeed creates a feed model.
func NewFeed() *Feed {
	return &Feed{
		proj:      presentation.NewProjector(),
		width:     DefaultWidth,
		height:    24,
		follow:    true,
		lines:     []string{},
		composer:  newComposer(),
		history:   newUserHistory(),
		permModal: newPermissionModal(),
		menu:      newSlashMenu(),
	}
}

// Init implements tea.Model. The composer owns focus by default.
func (m *Feed) Init() tea.Cmd {
	return nil
}

// Update processes messages.
func (m *Feed) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.onResize(msg)
	case agentEventBatchMsg:
		return m.applyBatch(msg.Events)
	case doneMsg:
		m.running = false
		m.busy = false
		m.runningTool = ""
		m.cancel = nil
		// The transient row ("Generating…", "canceling…") is over. A
		// failure is not transient: it enters the feed as a permanent,
		// scrollable line unless the loop already journaled a RunError.
		m.status = ""
		if msg.err != nil && !m.errorSeenSinceSend {
			m.addNotice(presentation.ItemError, errSummary(msg.err))
		}
		// A finished run returns focus to the composer (nothing else
		// claims it once the modal is closed).
		if !m.modalVisible && !m.composer.focused() {
			m.composer.focus()
		}
		return m, nil
	case tea.KeyMsg:
		return m.routeKey(msg)
	case permReplyMsg:
		if m.Approve != nil {
			m.Approve.Reply(msg.Decision)
		} else {
			m.addDiagnostic("permission reply failed: no approver")
		}
		m.decisionPending = false
		m.permModal.decisionPending = false
		return m, nil
	}
	return m, nil
}

// applyBatch processes a batch of events through the projector. The batch
// arrives as a Bubble Tea message on the event loop, never from a
// goroutine, so mutating model state here is safe.
func (m *Feed) applyBatch(events []agent.Event) (tea.Model, tea.Cmd) {
	for _, e := range events {
		if err := m.proj.Apply(e); err != nil {
			m.addDiagnostic(fmt.Sprintf("unable to project event %s seq=%d: %v", e.Type, e.Seq, err))
		}
		m.trackState(e)
		if e.Seq > m.lastSeq {
			m.lastSeq = e.Seq
		}
	}
	m.refresh()
	if m.modalVisible || m.decisionPending {
		// The feed keeps projecting behind the modal, but visible
		// auto-scroll pauses (Phase 2 decision).
		m.unseen++
	} else if m.follow {
		m.scrollToEnd()
	} else {
		// Browsing older output; count unseen updates.
		m.unseen++
	}
	return m, nil
}

// trackState keeps the permission modal in lockstep with the event stream:
// PermAsk opens it, PermReply/Interrupted close it. Run busy state is NOT
// derived from events here: RunStart/RunEnd are session boundaries (one per
// session), not per-turn boundaries, so the feed manages busy/running from
// trySend/doneMsg instead.
func (m *Feed) trackState(e agent.Event) {
	switch e.Type {
	case agent.RunError:
		m.errorSeenSinceSend = true
	case agent.ToolStart:
		if e.Call != nil {
			m.runningTool = e.Call.Name
		}
	case agent.ToolEnd:
		m.runningTool = ""
	case agent.PermAsk:
		m.runningTool = ""
		if !m.modalVisible && !m.decisionPending {
			m.followBeforeModal = m.follow
		}
		m.modalVisible = true
		m.pending = e.Call
		m.permModal.open(e.Call)
		// While the modal is visible the composer must not receive keys.
		if m.composer.focused() {
			m.composer.blur()
		}
	case agent.PermReply, agent.Interrupted:
		if e.Type == agent.Interrupted {
			m.errorSeenSinceSend = true
		}
		if e.Type == agent.PermReply && e.Decision != agent.Deny && e.Call != nil {
			m.runningTool = e.Call.Name
		}
		m.modalVisible = false
		m.decisionPending = false
		m.pending = nil
		m.permModal.close()
		m.follow = m.followBeforeModal
		if m.follow {
			m.scrollToEnd()
		}
		// The modal closed: restore composer focus. The composer owned
		// focus before the ask (typing a next draft during a run is
		// allowed); if a new PermAsk follows immediately, the next event
		// re-blurs it.
		if !m.composer.focused() {
			m.composer.focus()
		}
	}
}

// SendBatch is called from the batcher goroutine. It must not mutate the
// model off the Bubble Tea loop; the events are parked and delivered as a
// message instead. When no program is wired (tests drive Update directly)
// the batch is applied synchronously.
func (m *Feed) SendBatch(events []agent.Event) {
	if len(events) == 0 {
		return
	}
	if m.prog != nil {
		m.prog.Send(agentEventBatchMsg{Events: events})
		return
	}
	// Test path: no live program; apply directly. Tests call SendBatch from
	// the test goroutine only, so this is safe.
	m.applyBatch(events)
}

// SetProgram wires the running program so batcher flushes are delivered as
// messages instead of mutating the model off the event loop.
func (m *Feed) SetProgram(p *tea.Program) { m.prog = p }

// ProgramOptions returns the standard Bubble Tea options for running the full-screen Feed UI.
// It activates alternate-screen mode so full-height frames, viewport padding, and continuous
// redraws do not leak into the terminal's primary scrollback buffer.
func (m *Feed) ProgramOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithAltScreen(),
	}
}

// BuildFromEvents initializes the feed from a complete event list (replay
// or --continue). UI history is rebuilt from the live user_msg events only,
// so rewind-cancelled messages never enter history.
func (m *Feed) BuildFromEvents(events []agent.Event) {
	m.proj = presentation.NewProjector()
	m.notices = nil
	m.lastSeq = 0
	for _, e := range events {
		_ = m.proj.Apply(e)
		m.trackState(e)
		if e.Seq > m.lastSeq {
			m.lastSeq = e.Seq
		}
	}
	m.history.buildFromEvents(events)
	m.refresh()
	m.scrollToEnd()
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

// refresh rebuilds the visible lines from the projector plus UI notices.
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
	m.lines = renderItems(items, m.width)
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
	if m.composer.focused() {
		return m.composerKey(k)
	}
	return m.viewportKey(k)
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

// trySend implements the send policy:
//   - a slash command (/undo, /compact, /ctx, /edits, /rewind, /help) is
//     handled locally via the CLI callbacks, never sent to the model;
//   - empty/whitespace-only text: never send;
//   - no runner available: reject, keep the text, no history entry;
//   - a run already in flight: reject (no queue in Phase 3A), keep the
//     text, do not touch history;
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

// Message types used inside the feed.

// agentEventBatchMsg carries a batch of events from the batcher.
type agentEventBatchMsg struct {
	Events []agent.Event
}

// permReplyMsg carries a permission decision from the modal keys to the
// Update handler that forwards it to the approver.
type permReplyMsg struct {
	Decision agent.Decision
}
