package ui

import (
	"context"
	"fmt"
	"io"
	"os"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

// This file owns the Feed model itself: its state, its wiring, and the
// Event -> view projection. Layout and rendering live in feed_layout.go;
// the input router and every key handler live in feed_input.go.

// Defaults for the feed viewport.
const (
	maxVisibleFeedItems = 500
	maxUIDiagnostics    = 20
	maxUINotices        = 50
	minViewportWidth    = 20
)

// runFailedStatus is the transient row shown between a journaled terminal
// failure and doneMsg. ASCII only, like every other visible UI string.
const runFailedStatus = "run ended with an error"

// Feed is the projected, scrollable feed plus a multiline composer and a
// deterministic input router that arbitrates between the permission modal,
// the composer, the viewport and global shortcuts.
type Feed struct {
	proj       *presentation.Projector
	statusProj *presentation.StatusProjector

	// Viewport state.
	width          int
	height         int
	scrollTop      int // index of the first visible rendered line
	follow         bool
	unseen         int
	toolsExpanded  bool
	selectedItem   int
	navigationMode bool

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
	prog             *tea.Program
	testSyncDispatch bool

	// Touch and input settings.
	touchEnabled bool
	input        io.Reader

	// Per-item line cache: key is FeedItem.ID.
	lineCache   map[string]cacheEntry
	cacheWidth  int // width at which cache was populated; invalid on change
	renderCount int // test hook: counts actual renderItem calls

	// Render signature: deterministic fingerprint of the final rendered
	// output (m.lines), used by refresh to report whether the visible
	// output actually changed without cloning/comparing the slice.
	renderSig      uint64
	renderRows     int
	renderSigValid bool
}

// cacheEntry holds rendered lines for one feed item at a specific expansion state.
type cacheEntry struct {
	fp       uint64
	expanded bool
	lines    []string
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

// SetToolsExpanded sets the expanded state of tool output cards.
func (m *Feed) SetToolsExpanded(expanded bool) {
	if m.toolsExpanded != expanded {
		m.toolsExpanded = expanded
		m.refresh()
	}
}

// ToolsExpanded reports whether tool output cards are expanded.
func (m *Feed) ToolsExpanded() bool {
	return m.toolsExpanded
}

// NewFeed creates a feed model.
func NewFeed() *Feed {
	return &Feed{
		proj:         presentation.NewProjector(),
		statusProj:   presentation.NewStatusProjector(),
		width:        DefaultWidth,
		height:       24,
		follow:       true,
		selectedItem: -1,
		lines:        []string{},
		composer:     newComposer(),
		history:      newUserHistory(),
		permModal:    newPermissionModal(),
		menu:         newSlashMenu(),
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
	case tea.MouseMsg:
		return m.handleMouse(msg)
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
		if m.statusProj == nil {
			m.statusProj = presentation.NewStatusProjector()
		}
		m.statusProj.Apply(e)
		m.trackState(e)
		if e.Seq > m.lastSeq {
			m.lastSeq = e.Seq
		}
	}

	// refresh() runs the full render pipeline and reports whether the final
	// rendered output changed (fingerprint of m.lines), replacing the old
	// slices.Clone/slices.Equal snapshot comparison.
	displayChanged := m.refresh()
	if displayChanged {
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
	}
	return m, nil
}

// markRunFailed retires the progress claims the moment a terminal failure
// is projected. RunError/Interrupted mean the turn is over, but m.running
// is only cleared by doneMsg, which lands when the runner goroutine
// returns — so between the two the status row kept rendering
// "Generating…" over a dead run. m.busy is deliberately left untouched:
// it gates a second send, and only doneMsg proves the runner returned.
func (m *Feed) markRunFailed() {
	m.running = false
	m.runningTool = ""
	m.status = runFailedStatus
}

// trackState keeps the permission modal in lockstep with the event stream:
// PermAsk opens it, PermReply/Interrupted close it. Run busy state is NOT
// derived from events here: RunStart/RunEnd are session boundaries (one per
// session), not per-turn boundaries, so the feed manages busy/running from
// trySend/doneMsg instead. The one exception is a terminal failure, which
// retires the progress claims through markRunFailed.
func (m *Feed) trackState(e agent.Event) {
	switch e.Type {
	case agent.RunError:
		m.errorSeenSinceSend = true
		m.markRunFailed()
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
			m.markRunFailed()
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
	if m.testSyncDispatch {
		// Test path: no live program; apply directly. Tests call SendBatch
		// from the test goroutine only, so this is safe.
		m.applyBatch(events)
		return
	}
	// No program wired and not a test: drop the batch. The window is narrow
	// (a flush landing before the CLI calls SetProgram, or a Batcher.Stop()
	// flush after a failed launch) and losing it is recoverable — the journal
	// is the source of truth and the feed is rebuilt from it by
	// BuildFromEvents. Crashing the binary is not recoverable, so this path
	// must never panic.
	//
	// Nothing is mutated here on purpose: SendBatch runs on the batcher
	// goroutine, so recording a diagnostic would be a cross-goroutine write
	// to model state and a genuine data race.
}

// SetProgram wires the running program so batcher flushes are delivered as
// messages instead of mutating the model off the event loop.
func (m *Feed) SetProgram(p *tea.Program) { m.prog = p }

// SetTouch enables or disables finger-swipe touch scrolling in the Feed UI.
func (m *Feed) SetTouch(enabled bool) { m.touchEnabled = enabled }

// TouchEnabled reports whether touch scrolling is enabled.
func (m *Feed) TouchEnabled() bool { return m.touchEnabled }

// MouseEnabled reports whether mouse input is active in the viewport.
// Touch enables mouse cell motion, but NABD_NO_MOUSE overrides it.
func (m *Feed) MouseEnabled() bool {
	return m.touchEnabled && os.Getenv("NABD_NO_MOUSE") == ""
}

// SetInput overrides the input reader used by ProgramOptions. Defaults to os.Stdin.
func (m *Feed) SetInput(r io.Reader) { m.input = r }

// ProgramOptions returns the standard Bubble Tea options for running the full-screen Feed UI.
// It activates alternate-screen mode so full-height frames, viewport padding, and continuous
// redraws do not leak into the terminal's primary scrollback buffer.
// When touch scrolling is enabled via SetTouch, it enables mouse cell motion and wraps input
// with an SGRNormalizer to decode localized Arabic-Indic digit mouse reports.
// The NABD_NO_MOUSE environment variable disables mouse input entirely (overrides touch).
func (m *Feed) ProgramOptions() []tea.ProgramOption {
	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
	}
	if m.touchEnabled && os.Getenv("NABD_NO_MOUSE") == "" {
		opts = append(opts, tea.WithMouseCellMotion())
		in := m.input
		if in == nil {
			in = os.Stdin
		}
		opts = append(opts, tea.WithInput(NewSGRNormalizer(in)))
	}
	return opts
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
