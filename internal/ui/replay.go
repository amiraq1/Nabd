package ui

import (
	"fmt"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// maxGap caps dead air. The fixture has a four second pause; nobody sits
// through it, and replay is about order and meaning, not real duration.
const maxGap = 1500 * time.Millisecond

type tickMsg int // carries the index it was scheduled for

// Replay walks a session at roughly the speed it happened.
type Replay struct {
	events []agent.Event
	next   int
	width  int
	speed  float64
	paused bool
	done   bool
	buf    *string // text deltas in flight; pointer because Replay is a value model
}

// NewReplay takes the live branch, not the raw file: a compacted session
// replays from its summary forward, exactly as the model would see it.
func NewReplay(events []agent.Event, speed float64) Replay {
	if speed < 0 {
		speed = 0
	}
	return Replay{
		events: agent.Live(events),
		width:  DefaultWidth,
		speed:  speed,
		buf:    new(string),
	}
}

func (m Replay) Init() tea.Cmd { return m.step() }

// step emits the next event and schedules the one after it.
func (m Replay) step() tea.Cmd {
	if m.next >= len(m.events) {
		return nil
	}
	e := m.events[m.next]
	i := m.next

	var cmds []tea.Cmd
	if e.Type == agent.TextDelta {
		*m.buf += e.Text // same coalescing as Chat: one block, not one line per delta
	} else if s := flushJoin(m.buf, e, m.width); s != "" {
		cmds = append(cmds, tea.Println(s))
	}
	cmds = append(cmds, tea.Tick(m.delay(i), func(time.Time) tea.Msg {
		return tickMsg(i + 1)
	}))
	return tea.Batch(cmds...)
}

// delay is the wall gap to the following event, scaled and capped.
func (m Replay) delay(i int) time.Duration {
	if m.speed == 0 || i+1 >= len(m.events) {
		return time.Millisecond
	}
	d := m.events[i+1].Time.Sub(m.events[i].Time)
	if d < 0 {
		d = 0
	}
	d = time.Duration(float64(d) / m.speed)
	if d > maxGap {
		d = maxGap
	}
	if d < 40*time.Millisecond {
		d = 40 * time.Millisecond
	}
	return d
}

func (m Replay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w := msg.Width
		if w > 60 {
			w = 60 // a wide terminal is not a reason to lose the column
		}
		if w < 20 {
			w = DefaultWidth
		}
		m.width = w
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			// First press stops the replay; second one leaves. Stopping
			// and quitting are different intents and deserve two keys.
			if !m.done {
				m.done = true
				return m, tea.Println(dim.Render("⊘ stopped · press again to exit"))
			}
			return m, tea.Quit

		case " ":
			if !m.done {
				m.paused = !m.paused
				if !m.paused {
					return m, m.resume()
				}
			}
			return m, nil

		case "right", "l", "enter":
			if !m.done && m.paused {
				return m.advance()
			}
			return m, nil
		}
		return m, nil

	case tickMsg:
		if m.done || m.paused || int(msg) != m.next+1 {
			return m, nil // stale tick from before a pause or a stop
		}
		return m.advance()
	}
	return m, nil
}

func (m Replay) advance() (tea.Model, tea.Cmd) {
	m.next++
	if m.next >= len(m.events) {
		m.done = true
		if *m.buf != "" { // a session whose last event is text
			s := block(" ", *m.buf, m.width, lipgloss.NewStyle())
			*m.buf = ""
			return m, tea.Sequence(tea.Println(s), tea.Quit)
		}
		return m, tea.Quit
	}
	return m, m.step()
}

// resume re-arms the clock after a pause without replaying the current line.
func (m Replay) resume() tea.Cmd {
	i := m.next
	return tea.Tick(m.delay(i), func(time.Time) tea.Msg { return tickMsg(i + 1) })
}

// View is one status line. Everything above it was printed by tea.Println
// and belongs to the scrollback, which is what makes the output greppable
// after the program exits.
func (m Replay) View() string {
	n := m.next + 1
	if n > len(m.events) {
		n = len(m.events)
	}
	s := fmt.Sprintf("replay %d/%d", n, len(m.events))
	switch {
	case m.done:
		s += " · q"
	case m.paused:
		s += " · paused · space/→"
	}
	if p := partialTail(*m.buf, 6, m.width); p != "" {
		return p + "\n" + dim.Render(s)
	}
	return dim.Render(s)
}
