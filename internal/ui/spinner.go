package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// spinnerFrames returns the animation frames for the working indicator.
// ASCII-only to satisfy the UI string literal whitelist (ascii_guard_test).
func spinnerFrames() []string {
	return []string{"|", "/", "-", "\\"}
}

// spinTickMsg advances the spinner one frame.
type spinTickMsg struct{}

// spinTick returns a command that ticks every 150ms while running.
func spinTick() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return spinTickMsg{}
	})
}

// spinnerView returns the current spinner frame styled for the status line.
func (m *Chat) spinnerView() string {
	frames := spinnerFrames()
	return dim.Render(frames[m.spinFrame%len(frames)] + " working - ctrl+c to cancel")
}
