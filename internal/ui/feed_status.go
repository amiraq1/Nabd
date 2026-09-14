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
	// Compaction is background work that outlives a turn, so it is
	// reported outside the run gate.
	if m.statusProj != nil && m.statusProj.Status().Phase == presentation.PhaseCompacting {
		return "Compacting context…"
	}

	// Everything below claims progress, so it requires a live run.
	//
	// StatusProjector deletes a tool from its map on ToolEnd only:
	// Interrupted, RunError and RunEnd leave it in place. Reading
	// ActiveTools without this gate therefore reports a tool that stopped
	// when the run was canceled, forever, on an idle feed.
	if !m.running && !m.busy {
		return ""
	}
	if m.statusProj != nil {
		switch active := m.statusProj.Status().ActiveTools; len(active) {
		case 0:
		case 1:
			return "Running " + active[0].Name + "…"
		default:
			return fmt.Sprintf("Running %d tools…", len(active))
		}
	}
	if m.running {
		if !m.streamFirstDeltaAt.IsZero() {
			return ""
		}
		return "Generating…"
	}
	return "Working…"
}
