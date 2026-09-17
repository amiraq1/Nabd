package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"nabd/internal/agent"
	"nabd/internal/presentation"
	"nabd/internal/providercmd"

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
	runner         Runner
	events         <-chan agent.Event
	width          int
	input          string
	buf            string
	running        bool
	cancel         context.CancelFunc
	status         string
	statusProj     *presentation.StatusProjector
	Approve        *Approver
	pending        *agent.ToolCall
	callbacks      SessionCallbacks
	secretPrompt   bool
	secretProvider string
	secretKey      string
}

func NewChat(r Runner, events <-chan agent.Event) *Chat {
	return &Chat{runner: r, events: events, width: DefaultWidth, Approve: NewApprover(), statusProj: presentation.NewStatusProjector()}
}

// SetCallbacks wires the command hooks, the same contract the Feed uses.
func (m *Chat) SetCallbacks(cb *SessionCallbacks) {
	if cb != nil {
		m.callbacks = *cb
	}
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
		if m.statusProj == nil {
			m.statusProj = presentation.NewStatusProjector()
		}
		m.statusProj.Apply(e)
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

	case modelsResultMsg:
		// The /models probe settled off the event loop. The fetch held
		// running/cancel, so releasing them here is what re-arms Ctrl+C and
		// the send gate.
		m.running = false
		m.cancel = nil
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.status = "canceled"
				return m, nil
			}
			m.status = fmt.Sprintf("nabd models: provider_%s: %v", providercmd.KindOf(msg.err), msg.err)
			return m, nil
		}
		var b strings.Builder
		for _, mod := range msg.models {
			b.WriteString(mod + "\n")
		}
		if msg.disclaimer != "" {
			b.WriteString(msg.disclaimer)
		}
		m.status = strings.TrimRight(b.String(), "\n")
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
	if m.secretPrompt {
		switch k.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.secretPrompt = false
			m.secretProvider = ""
			m.secretKey = ""
			m.status = "connect canceled"
			return m, nil
		case tea.KeyBackspace:
			if r := []rune(m.secretKey); len(r) > 0 {
				m.secretKey = string(r[:len(r)-1])
			}
			return m, nil
		case tea.KeyCtrlU:
			m.secretKey = ""
			return m, nil
		case tea.KeyEnter:
			key := strings.TrimSpace(m.secretKey)
			provID := m.secretProvider
			m.secretPrompt = false
			m.secretProvider = ""
			m.secretKey = ""
			if key == "" {
				m.status = "empty key; nothing written"
				return m, nil
			}
			if m.callbacks.OnConnect == nil {
				m.status = "connect not supported in this version"
				return m, nil
			}
			summary, err := m.callbacks.OnConnect(provID, key)
			if err != nil {
				m.status = "connect failed: " + err.Error()
				return m, nil
			}
			m.status = summary
			return m, nil
		case tea.KeySpace:
			m.secretKey += " "
			return m, nil
		case tea.KeyRunes:
			m.secretKey += string(k.Runes)
			return m, nil
		}
		return m, nil
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
			status, cmd := m.command(line)
			m.status = status
			return m, cmd
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
	if m.secretPrompt {
		line := "API key (input hidden): ▌"
		if m.status != "" {
			line = dim.Render("· "+m.status) + "\n" + line
		}
		return fmt.Sprint(line)
	}
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

// command executes one slash command line and returns the status line to show
// plus, for commands with an asynchronous leg, a tea.Cmd for Bubble Tea to run
// off the event loop. /models performs network I/O: running it inside Update
// froze the renderer and the Ctrl+C handler, so it now goes through a tea.Cmd
// with a cancellable context, exactly as the run path does. running/cancel are
// reused rather than adding new state, so the existing Ctrl+C and send gates
// cover a fetch in flight too.
func (m *Chat) command(line string) (string, tea.Cmd) {
	parsed := ParseSlashCommand(line)
	if !parsed.Valid {
		return parsed.Error, nil
	}
	switch parsed.Command.Name {
	case "/rewind":
		if m.callbacks.OnRewind == nil {
			return "rewind not supported in this version", nil
		}
		restored, status := m.callbacks.OnRewind(parsed.N)
		m.SetInput(restored)
		if status == "" {
			status = "rewound"
		}
		return status, nil
	case "/ctx":
		if m.callbacks.OnCtx == nil {
			return "—", nil
		}
		return m.callbacks.OnCtx(), nil
	case "/compact":
		if m.callbacks.OnCompact == nil {
			return "—", nil
		}
		return m.callbacks.OnCompact(), nil
	case "/undo":
		if m.callbacks.OnUndo == nil {
			return "undo not supported in this version", nil
		}
		return m.callbacks.OnUndo(parsed.N), nil
	case "/edits":
		if m.callbacks.OnEdits == nil {
			return "—", nil
		}
		return m.callbacks.OnEdits(), nil
	case "/help":
		return CommandHelp(m.width), nil
	case "/provider":
		if m.callbacks.OnProvider == nil {
			return "provider not supported in this version", nil
		}
		return m.callbacks.OnProvider(), nil
	case "/models":
		if m.callbacks.OnModels == nil {
			return "models not supported in this version", nil
		}
		m.running = true
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		return "fetching models… · ctrl+c to cancel", func() tea.Msg {
			models, disclaimer, err := m.callbacks.OnModels(ctx, parsed.Arg)
			cancel()
			return modelsResultMsg{provider: parsed.Arg, models: models, disclaimer: disclaimer, err: err}
		}
	case "/connect":
		if m.callbacks.OnConnect == nil {
			return "connect not supported in this version", nil
		}
		m.secretPrompt = true
		m.secretProvider = parsed.Arg
		m.secretKey = ""
		return "enter API key for " + parsed.Arg + " (input hidden)", nil
	}
	return "unknown command: " + parsed.RawCmd, nil
}

func (m *Chat) SetInput(s string) {
	m.input = s
}

// Status returns the current status line (for tests).
func (m *Chat) Status() string { return m.status }

// Command parses and executes a slash command (for testing/parity). A command
// with an asynchronous leg is settled synchronously here: this helper is not
// the event loop, so blocking on the probe is acceptable and lets the parity
// tests assert the final status instead of an intermediate one.
//
// It is a test convenience, not the production path: it runs the command the
// way the UI never does (the UI hands the tea.Cmd back to Bubble Tea). It must
// not be read as evidence that Chat and Feed schedule or present /models
// identically — what they share is the command contract, not the scheduling
// and not the rendering of the result.
func (m *Chat) Command(line string) string {
	status, cmd := m.command(line)
	m.status = status
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	return m.status
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
