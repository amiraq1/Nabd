package ui

import (
	"context"
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// runnerHooks records whether the context was cancelled.
type runnerHooks struct {
	mu        sync.Mutex
	cancelled bool
}

func (r *runnerHooks) Run(ctx context.Context, text string) error {
	<-ctx.Done()
	r.mu.Lock()
	r.cancelled = true
	r.mu.Unlock()
	return nil
}

// TestCtrlCHandlerCancelsNotQuits pins the exact Band-2 concern at the UI
// layer: the first ctrl+c while running must cancel the context and return
// m, nil — never tea.Quit. A quit would terminate the program before the
// channel drains, swallowing the buffered paragraph.
func TestCtrlCHandlerCancelsNotQuits(t *testing.T) {
	ch := make(chan agent.Event, 8)
	r := &runnerHooks{}
	m := NewChat(r, ch)

	// Type a question, then Enter to start the turn.
	mdl, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("سؤال")})
	m = asChat(t, mdl)
	mdl, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asChat(t, mdl)
	if cmd == nil {
		t.Fatal("Enter produced no command — the run never started")
	}
	if !m.running {
		t.Fatal("model should be running after Enter")
	}

	// Execute the Enter command: that is what starts the runner (which then
	// blocks on ctx.Done). In the real program Bubble Tea does this.
	cmdDone := make(chan struct{})
	go func() {
		cmd()
		close(cmdDone)
	}()
	// Give the runner a moment to enter its ctx.Done wait.
	time.Sleep(50 * time.Millisecond)

	// First ctrl+c while running.
	mdl2, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m2 := asChat(t, mdl2)
	if !m2.running {
		t.Error("first ctrl+c stopped the run — must only cancel, not quit")
	}
	if cmd2 != nil {
		t.Errorf("first ctrl+c returned a command (%v) — must be nil, never tea.Quit", cmd2)
	}

	// The cancellation must have fired and the runner returned.
	select {
	case <-cmdDone:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not return after ctrl+c cancellation")
	}
	r.mu.Lock()
	cancelled := r.cancelled
	r.mu.Unlock()
	if !cancelled {
		t.Error("ctrl+c did not cancel the runner context")
	}
}

// TestCtrlCSecondPressQuits: after the run finished (running=false), ctrl+c
// is the quit key.
func TestCtrlCSecondPressQuits(t *testing.T) {
	ch := make(chan agent.Event, 8)
	m := NewChat(runnerStub{}, ch)

	mdl, _ := m.Update(doneMsg{err: nil}) // turn over, not running
	m = asChat(t, mdl)
	if m.running {
		t.Fatal("expected not running after doneMsg")
	}

	mdl2, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	_ = mdl2
	if cmd2 == nil {
		t.Fatal("ctrl+c when idle must return tea.Quit")
	}
	// tea.Quit is a Cmd; verify it produces a QuitMsg.
	msg := cmd2()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c when idle produced %T, want tea.QuitMsg", msg)
	}
}
