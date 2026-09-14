package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// runtimeThroughputInterval is the minimum wall-clock gap between
// throughput refreshes. The batcher fires every 20 ms and each refresh
// costs ~1.2–2 ms on mobile CPU, so without a throttle we would burn
// ~10 % of the frame budget on the status number alone.
const runtimeThroughputInterval = 200 * time.Millisecond

// runtimeThroughputText formats the measured throughput status line (one line,
// no newlines). It is called from runtimeStatusText and never touches m.status
// or rankHint — the status line belongs to phase text and security prompts,
// so throughput must never compete with them on the status stack.
//
// Output ladder (widest first, strict first fit):
//
//	width >= 41: "TTFT 1.24s · 42.1 tok/s · 312 tok" (or "est ..." when live)
//	width >= 27: "TTFT 1.24s · 42.1 tok/s" (or "est ..." when live)
//	width >= 10: "42.1 tok/s" (or "est ..." when live)
//	width <= 20: "" (blank is more honest than truncation like "42.1 to")
//
// Degradation drops total tokens first, then TTFT, then the rate entirely.
// When idle (!m.running && !m.busy), returns "" so no stale numbers linger.
func (m *Feed) runtimeThroughputText(width int) string {
	// Disappear completely when no live run is active (Phase 1 contract).
	if !m.running && !m.busy {
		return ""
	}
	// Blank at 20 columns or less — dropping is cleaner than truncation.
	if width <= 20 {
		return ""
	}
	// No delta arrived yet: TTFT is not yet determined.
	if m.firstDeltaAt.IsZero() {
		return ""
	}

	ttft := m.firstDeltaAt.Sub(m.reqStartedAt)
	if ttft < 0 {
		ttft = 0
	}
	ttftStr := formatDuration(ttft)

	var (
		rateStr string
		tokStr  string
	)

	if !m.lastDeltaAt.IsZero() {
		streamElapsed := m.lastDeltaAt.Sub(m.firstDeltaAt)
		if streamElapsed > 0 {
			tokens := m.completionTokens()
			if tokens > 0 {
				// Final measured rate: uses provider-reported token count
				// over the same stream interval (lastDeltaAt - firstDeltaAt),
				// excluding the first token from numerator because it marks
				// the start boundary.
				if tokens > 1 {
					rateTok := float64(tokens-1) / streamElapsed.Seconds()
					rateStr = fmt.Sprintf("%.1f tok/s", rateTok)
				} else {
					rateStr = "0.0 tok/s"
				}
				tokStr = fmt.Sprintf("%d tok", tokens)
			} else {
				// Live estimate: streamedChars / 4 over stream interval.
				// Visual distinction: prefix 'est ' because '~' is not in AllowedUISymbols.
				if m.cachedLiveRate != "" && m.cachedLiveTok != "" {
					rateStr = m.cachedLiveRate
					tokStr = m.cachedLiveTok
				} else if m.streamedChars > 0 {
					estTok := float64(m.streamedChars) / 4.0
					rateTok := estTok / streamElapsed.Seconds()
					rateStr = fmt.Sprintf("est %.1f tok/s", rateTok)
					tokStr = fmt.Sprintf("est %d tok", int(estTok))
				}
			}
		}
	}

	// Candidate ladder: widest first, strict first fit.
	var candidates []string
	if rateStr != "" && tokStr != "" {
		candidates = append(candidates, fmt.Sprintf("TTFT %s · %s · %s", ttftStr, rateStr, tokStr))
	}
	if rateStr != "" {
		candidates = append(candidates, fmt.Sprintf("TTFT %s · %s", ttftStr, rateStr))
		candidates = append(candidates, rateStr)
	}
	if rateStr == "" && tokStr == "" {
		candidates = append(candidates, fmt.Sprintf("TTFT %s", ttftStr))
	}

	for _, c := range candidates {
		if ansi.StringWidth(c) <= width {
			return c
		}
	}
	return ""
}

// completionTokens returns the completion tokens reported by the provider
// through the status projector, or 0 if not yet available.
func (m *Feed) completionTokens() int {
	if m.statusProj == nil {
		return 0
	}
	return m.statusProj.Status().Usage.CompletionTokens
}

// formatDuration formats a duration as a human-friendly seconds string (e.g. 1.24s).
func formatDuration(d time.Duration) string {
	secs := d.Seconds()
	if secs < 0.01 {
		return "0.00s"
	}
	return fmt.Sprintf("%.2fs", secs)
}
