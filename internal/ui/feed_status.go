package ui

import (
	"fmt"

	"nabd/internal/presentation"
)

const (
	rankHint         = 1
	rankResult       = 2
	rankRunLifecycle = 3
)

func (m *Feed) setStatus(text string, rank int) {
	if text == "" {
		m.clearStatus()
		return
	}
	if rank >= m.statusRank {
		m.status = text
		m.statusRank = rank
	}
}

func (m *Feed) clearStatus() {
	m.status = ""
	m.statusRank = 0
}

func (m *Feed) phaseText() string {
	if m.statusProj != nil {
		s := m.statusProj.Status()
		if len(s.ActiveTools) == 1 {
			return "Running " + s.ActiveTools[0].Name + "…"
		}
		if len(s.ActiveTools) > 1 {
			return fmt.Sprintf("Running %d tools…", len(s.ActiveTools))
		}
		switch s.Phase {
		case presentation.PhaseCompacting:
			return "Compacting context…"
		}
	}
	if m.running {
		return "Generating…"
	}
	if m.busy {
		return "Working…"
	}
	return ""
}
