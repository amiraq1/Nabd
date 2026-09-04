package ui

import (
	"context"
	"fmt"
	"strings"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// Runner is the loop, seen from the UI: one message in, events out.
type Runner interface {
	Run(ctx context.Context, text string) error
}

type evMsg agent.Event
type doneMsg struct{ err error }

// Chat is a single-line prompt with a scrollback of printed events.
// Deliberately not a textarea: one line, one hand, one thumb.
type Chat struct {
	runner    Runner
	events    <-chan agent.Event
	width     int
	input     string
	buf       string
	running   bool
	cancel    context.CancelFunc
	status    string
	Approve   *Approver
	pending   *agent.ToolCall
	OnUndo    func(n int) string
	OnRewind  func(n int) string
	OnCtx     func() string
	OnCompact func() string
	OnEdits   func() string
}

func NewChat(r Runner, events <-chan agent.Event) *Chat {
	return &Chat{runner: r, events: events, width: DefaultWidth, Approve: NewApprover()}
}

func (m *Chat) Init() tea.Cmd { return waitEvent(m.events) }

// waitEvent pumps one event per command: Bubble Tea owns the goroutine,
// so nothing in the UI touches a channel outside Update.
func waitEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return evMsg(e)
	}
}

func (m *Chat) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w := msg.Width
		if w > 60 {
			w = 60
		}
		if w < 20 {
			w = DefaultWidth
		}
		m.width = w
		return m, nil

	case evMsg:
		e := agent.Event(msg)
		if e.Type == agent.TextDelta {
			m.buf += e.Text
			return m, waitEvent(m.events)
		}
		switch e.Type {
		case agent.PermAsk:
			m.pending = e.Call
		case agent.PermReply, agent.Interrupted:
			m.pending = nil
		}
		var cmds []tea.Cmd
		if s := flushJoin(&m.buf, e, m.width); s != "" {
			// One Println per Update: tea.Batch runs commands concurrently,
			// so two Printlns would race for the terminal.
			cmds = append(cmds, tea.Println(s))
		}
		cmds = append(cmds, waitEvent(m.events))
		return m, tea.Batch(cmds...)

	case doneMsg:
		m.running = false
		m.cancel = nil
		m.status = ""
		if msg.err != nil {
			m.status = "error: " + errSummary(msg.err)
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Chat) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pending != nil {
		switch k.String() {
		case "y", "Y":
			m.pending = nil
			m.Approve.Reply(agent.AllowOnce)
			return m, nil
		case "a", "A":
			m.pending = nil
			m.Approve.Reply(agent.AllowSession)
			return m, nil
		case "n", "N", "esc":
			m.pending = nil
			m.Approve.Reply(agent.Deny)
			return m, nil
		case "ctrl+c":
			if m.running && m.cancel != nil {
				m.cancel()
				m.status = "canceling…"
			}
			return m, nil
		default:
			return m, nil // no typing while prompt is pending
		}
	}
	switch k.Type {
	case tea.KeyCtrlC:
		// First ctrl+c cancels the turn; it never quits mid-flight,
		// because losing a half-finished answer to a fat thumb is cruel.
		if m.running && m.cancel != nil {
			m.cancel()
			m.status = "canceling…"
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyCtrlD:
		if m.running {
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyEnter:
		line := strings.TrimSpace(m.input)
		if line == "" {
			return m, nil
		}
		if strings.HasPrefix(line, "/") {
			if m.running {
				m.status = "wait for turn to finish"
				return m, nil
			}
			m.input = ""
			m.status = m.command(line)
			return m, nil
		}
		if m.running {
			m.status = "wait for turn to finish · your text is kept"
			return m, nil
		}
		text := line
		m.input = ""
		m.running = true
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		return m, func() tea.Msg {
			err := m.runner.Run(ctx, text)
			cancel()
			return doneMsg{err}
		}

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

// View is the prompt line only. Everything else lives in the scrollback,
// which is what lets you scroll back with your thumb and grep it later.
func (m *Chat) View() string {
	if m.running && m.pending == nil {
		s := "· working · ctrl+c to cancel"
		if m.status != "" {
			s = "· " + m.status
		}
		if m.input != "" {
			s += "\n› " + m.input
		}
		if p := partialTail(m.buf, 6, m.width); p != "" {
			return p + "\n" + dim.Render(s)
		}
		return dim.Render(s)
	}
	line := "› " + m.input + "▌"
	if m.status != "" {
		line = dim.Render("· "+m.status) + "\n" + line
	}
	if m.pending != nil {
		keys := "y allow once · a allow session · n deny"
		if m.pending.Name == "bash" {
			keys = "y allow once · n deny · (no session allow for commands)"
		}
		return line + "\n" + warn.Render(keys)
	}
	return fmt.Sprint(line)
}

func (m *Chat) command(line string) string {
	parsed := ParseSlashCommand(line)
	if !parsed.Valid {
		return parsed.Error
	}
	switch parsed.Command.Name {
	case "/rewind":
		if m.OnRewind == nil {
			return "rewind not supported in this version"
		}
		return m.OnRewind(parsed.N)
	case "/ctx":
		if m.OnCtx == nil {
			return "—"
		}
		return m.OnCtx()
	case "/compact":
		if m.OnCompact == nil {
			return "—"
		}
		return m.OnCompact()
	case "/undo":
		if m.OnUndo == nil {
			return "undo not supported in this version"
		}
		return m.OnUndo(parsed.N)
	case "/edits":
		if m.OnEdits == nil {
			return "—"
		}
		return m.OnEdits()
	case "/help":
		return "/undo [n] · /edits · /rewind [n] · /ctx · /compact · ctrl+c · ctrl+d"
	}
	return "unknown command: " + parsed.RawCmd
}

func (m *Chat) SetInput(s string) {
	m.input = s
}

// errSummary formats a runtime error for the UI status bar while ensuring no
// non-ASCII runes outside AllowedUISymbols leak into the interface.
func errSummary(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	for _, r := range s {
		if r >= 128 && !AllowedUISymbols[r] {
			return "execution failed"
		}
	}
	return s
}
