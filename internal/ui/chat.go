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
	runner  Runner
	events  <-chan agent.Event
	width   int
	input   string
	running bool
	cancel  context.CancelFunc
	status  string
	quit    bool
	Approve *Approver
	pending *agent.ToolCall
}

func NewChat(r Runner, events <-chan agent.Event) Chat {
	return Chat{runner: r, events: events, width: DefaultWidth, Approve: NewApprover()}
}

func (m Chat) Init() tea.Cmd { return waitEvent(m.events) }

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

func (m Chat) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		switch e.Type {
		case agent.PermAsk:
			m.pending = e.Call
		case agent.PermReply, agent.Interrupted, agent.RunEnd:
			m.pending = nil
		}
		var cmds []tea.Cmd
		if s := RenderEvent(e, m.width); s != "" {
			cmds = append(cmds, tea.Println(s))
		}
		cmds = append(cmds, waitEvent(m.events))
		return m, tea.Batch(cmds...)

	case doneMsg:
		m.running = false
		m.cancel = nil
		m.status = ""
		if msg.err != nil {
			m.status = "خطأ"
		}
		if m.quit {
			return m, tea.Quit
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Chat) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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
				m.status = "يُلغى…"
			}
			return m, nil
		default:
			return m, nil // لا كتابة أثناء السؤال
		}
	}
	switch k.Type {
	case tea.KeyCtrlC:
		// First ctrl+c cancels the turn; it never quits mid-flight,
		// because losing a half-finished answer to a fat thumb is cruel.
		if m.running && m.cancel != nil {
			m.cancel()
			m.status = "يُلغى…"
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyCtrlD:
		if m.running {
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyEnter:
		text := strings.TrimSpace(m.input)
		if text == "" || m.running {
			return m, nil
		}
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
func (m Chat) View() string {
	if m.running && m.pending == nil {
		s := "· يعمل · ctrl+c للإلغاء"
		if m.status != "" {
			s = "· " + m.status
		}
		return dim.Render(s)
	}
	line := "› " + m.input + "▌"
	if m.status != "" {
		line = dim.Render("· "+m.status) + "\n" + line
	}
	if m.pending != nil {
		return line + "\n" + warn.Render("y سماح مرة · a سماح للجلسة · n رفض")
	}
	return fmt.Sprint(line)
}
