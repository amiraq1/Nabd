package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// DiagnosticSnapshot contains aggregate UI telemetry only. It deliberately
// excludes prompts, tool arguments, output, paths, errors, and journal data.
type DiagnosticSnapshot struct {
	Items        int
	RenderedRows int
	CachedItems  int
	RenderCalls  int
	Notices      int
	Diagnostics  int
	Unseen       int
	Width        int
	Height       int
	Follow       bool
	Expanded     bool
	Mode         WidthMode
}

func (m *Feed) diagnosticSnapshot() DiagnosticSnapshot {
	items := 0
	if m.proj != nil {
		items = len(m.proj.Items())
	}
	return DiagnosticSnapshot{
		Items:        items,
		RenderedRows: len(m.lines),
		CachedItems:  len(m.lineCache),
		RenderCalls:  m.renderCount,
		Notices:      len(m.notices),
		Diagnostics:  len(m.diagnostics),
		Unseen:       m.unseen,
		Width:        m.width,
		Height:       m.height,
		Follow:       m.follow,
		Expanded:     m.toolsExpanded,
		Mode:         widthMode(m.width),
	}
}

// Diagnostics renders a bounded, copyable, privacy-safe dashboard.
func (m *Feed) Diagnostics(width int) string {
	if width < minViewportWidth {
		width = minViewportWidth
	}
	s := m.diagnosticSnapshot()
	follow, expanded := "off", "off"
	if s.Follow {
		follow = "on"
	}
	if s.Expanded {
		expanded = "on"
	}
	lines := []string{"Diagnostics"}
	if widthMode(width) == WidthNarrow {
		lines = append(lines,
			fmt.Sprintf("items %d · rows %d", s.Items, s.RenderedRows),
			fmt.Sprintf("cache %d · renders %d", s.CachedItems, s.RenderCalls),
			fmt.Sprintf("follow %s · unseen %d", follow, s.Unseen),
		)
	} else {
		lines = append(lines,
			fmt.Sprintf("feed: %d items · %d rendered rows · %d unseen", s.Items, s.RenderedRows, s.Unseen),
			fmt.Sprintf("cache: %d items · %d render calls", s.CachedItems, s.RenderCalls),
			fmt.Sprintf("layout: %s · %dx%d · follow %s · tools expanded %s", s.Mode, s.Width, s.Height, follow, expanded),
			fmt.Sprintf("ui: %d notices · %d diagnostics", s.Notices, s.Diagnostics),
		)
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return strings.Join(lines, "\n")
}
