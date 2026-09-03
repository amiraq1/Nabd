package ui

import (
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
	minViewportWidth    = 20
)

// Feed is the new UI model: a scrollable viewport over a projected feed.
type Feed struct {
	proj *presentation.Projector

	// Viewport state.
	width     int
	height    int
	scrollTop int // index of first visible item
	follow    bool
	unseen    int
	modal     bool // permission modal visible

	// Cached rendered lines for the current viewport.
	lines []string

	// UI diagnostics (not written to journal).
	diagnostics []string

	// Header info.
	header string

	// Callbacks wired by the CLI.
	callbacks FeedCallbacks

	// Composer state (kept minimal; full composer is Phase 3).
	input   string
	running bool
	status  string
	pending *agent.ToolCall
}

// FeedCallbacks holds the hooks the feed uses to talk back to the loop.
type FeedCallbacks struct {
	OnUndo    func(n int) string
	OnCompact func() string
}

// SetHeader sets the header line shown above the viewport.
func (m *Feed) SetHeader(h string) { m.header = h }

// SetCallbacks wires the undo/compact hooks.
func (m *Feed) SetCallbacks(cb *FeedCallbacks) {
	if cb != nil {
		m.callbacks = *cb
	}
}

// SendBatch delivers a batch of events to the feed.
func (m *Feed) SendBatch(events []agent.Event) {
	if len(events) == 0 {
		return
	}
	m.applyBatch(events)
}

// BuildFromEvents initializes the feed from a complete event list (replay).
func (m *Feed) BuildFromEvents(events []agent.Event) {
	m.proj = presentation.NewProjector()
	for _, e := range events {
		_ = m.proj.Apply(e)
	}
	m.refresh()
	m.scrollToEnd()
}

// Composer state (kept minimal; full composer is Phase 3).
//
//nolint:unused // kept for Phase 3 composer integration
//
//nolint:structcheck

// NewFeed creates a feed model.
func NewFeed() *Feed {
	return &Feed{
		proj:   presentation.NewProjector(),
		width:  DefaultWidth,
		height: 24,
		follow: true,
		lines:  []string{},
	}
}

// Init implements tea.Model.
func (m *Feed) Init() tea.Cmd {
	return nil
}

// Update processes messages.
func (m *Feed) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		// Use the available terminal width, clamped to a sane minimum. The
		// viewport is NOT capped at 60 columns — that was a holdover from the
		// old single-line prompt. Wide terminals get a wide feed.
		if m.width < minViewportWidth {
			m.width = minViewportWidth
		}
		m.height = msg.Height
		m.refresh()
		return m, nil

	case agentEventBatchMsg:
		return m.applyBatch(msg.Events)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// applyBatch processes a batch of events through the projector.
func (m *Feed) applyBatch(events []agent.Event) (tea.Model, tea.Cmd) {
	for _, e := range events {
		if err := m.proj.Apply(e); err != nil {
			m.addDiagnostic(fmt.Sprintf("unable to project event %s seq=%d: %v", e.Type, e.Seq, err))
		}
	}
	m.refresh()
	switch {
	case m.modal:
		// Feed updates logically behind the modal, but visible auto-scroll pauses.
		m.unseen++
	case m.follow:
		m.scrollToEnd()
	default:
		// User is browsing history; count unseen updates.
		m.unseen++
	}
	return m, nil
}

// addDiagnostic records a UI-side diagnostic (not written to journal).
func (m *Feed) addDiagnostic(text string) {
	m.diagnostics = append(m.diagnostics, text)
	if len(m.diagnostics) > maxUIDiagnostics {
		m.diagnostics = m.diagnostics[len(m.diagnostics)-maxUIDiagnostics:]
	}
}

// refresh rebuilds the visible lines from the projector.
func (m *Feed) refresh() {
	items := m.proj.Items()
	if len(items) > maxVisibleFeedItems {
		items = items[len(items)-maxVisibleFeedItems:]
	}
	m.lines = renderItems(items, m.width)
}

// scrollToEnd moves the viewport to show the latest items.
func (m *Feed) scrollToEnd() {
	m.scrollTop = 0
	m.unseen = 0
}

// visibleHeight returns how many lines the viewport can show.
func (m *Feed) visibleHeight() int {
	// Reserve space for header (1) + status (1) + composer (2).
	return max(1, m.height-4)
}

// View renders the full screen.
func (m *Feed) View() string {
	var b strings.Builder

	// Header.
	if m.header != "" {
		b.WriteString(m.header)
		b.WriteByte('\n')
	}

	// Viewport.
	vh := m.visibleHeight()
	start := m.scrollTop
	end := min(start+vh, len(m.lines))
	if start < len(m.lines) {
		for i := start; i < end; i++ {
			b.WriteString(m.lines[i])
			b.WriteByte('\n')
		}
	}

	// Unseen indicator.
	if m.unseen > 0 && !m.follow {
		b.WriteString(dim.Render(fmt.Sprintf("v %d updates", m.unseen)))
		b.WriteByte('\n')
	}

	// Status line.
	if m.status != "" {
		b.WriteString(dim.Render("· " + m.status))
		b.WriteByte('\n')
	}

	// Composer.
	b.WriteString("› " + m.input + "▌")

	return b.String()
}

// handleKey processes keyboard input.
func (m *Feed) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Permission modal has priority.
	if m.pending != nil {
		switch k.String() {
		case "y", "Y":
			m.pending = nil
			// Approve via the existing approver mechanism.
			return m, func() tea.Msg { return agentReplyMsg{agent.AllowOnce} }
		case "a", "A":
			m.pending = nil
			return m, func() tea.Msg { return agentReplyMsg{agent.AllowSession} }
		case "n", "N", "esc":
			m.pending = nil
			return m, func() tea.Msg { return agentReplyMsg{agent.Deny} }
		case "ctrl+c":
			return m, nil
		}
		return m, nil
	}

	switch k.Type {
	case tea.KeyCtrlC:
		if m.running {
			m.status = "canceling…"
			return m, func() tea.Msg { return interruptMsg{} }
		}
		return m, tea.Quit

	case tea.KeyCtrlD:
		if m.running {
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyUp:
		m.follow = false
		m.scrollTop = min(m.scrollTop+1, max(0, len(m.lines)-m.visibleHeight()))
		return m, nil

	case tea.KeyDown:
		m.scrollTop = max(m.scrollTop-1, 0)
		if m.scrollTop == 0 {
			m.follow = true
			m.unseen = 0
		}
		return m, nil

	case tea.KeyPgUp:
		m.follow = false
		m.scrollTop = min(m.scrollTop+m.visibleHeight(), max(0, len(m.lines)-m.visibleHeight()))
		return m, nil

	case tea.KeyPgDown:
		m.scrollTop = max(m.scrollTop-m.visibleHeight(), 0)
		if m.scrollTop == 0 {
			m.follow = true
			m.unseen = 0
		}
		return m, nil

	case tea.KeyHome:
		m.follow = false
		m.scrollTop = max(0, len(m.lines)-m.visibleHeight())
		return m, nil

	case tea.KeyEnd:
		m.follow = true
		m.unseen = 0
		m.scrollTop = 0
		return m, nil

	case tea.KeyEnter:
		line := strings.TrimSpace(m.input)
		if line == "" {
			return m, nil
		}
		m.input = ""
		m.running = true
		m.status = ""
		return m, func() tea.Msg { return userSubmitMsg{Text: line} }

	case tea.KeyBackspace:
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
		return m, nil

	case tea.KeyCtrlU:
		m.input = ""
		return m, nil

	case tea.KeySpace:
		m.input += " "
		return m, nil

	case tea.KeyRunes:
		m.input += string(k.Runes)
		return m, nil
	}
	return m, nil
}

// agentEventBatchMsg carries a batch of events from the batcher.
type agentEventBatchMsg struct {
	Events []agent.Event
}

// agentReplyMsg carries a permission decision.
type agentReplyMsg struct {
	Decision agent.Decision
}

// interruptMsg signals cancellation.
type interruptMsg struct{}

// userSubmitMsg carries user input.
type userSubmitMsg struct {
	Text string
}
