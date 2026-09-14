package ui

import (
	"fmt"
	"time"
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
// Output ladder:
// At widths above 20, choose the first candidate whose visual width fits;
// otherwise return blank. Dropping completely is cleaner and more honest
// than mid-token truncation (e.g. "42.1 to").
//
// Candidates in order (widest first):
//  1. "TTFT 1.24s · 42.1 tok/s · 312 tok" (or "est ..." when live)
//  2. "TTFT 1.24s · 42.1 tok/s" (or "est ..." when live)
//  3. "42.1 tok/s" (or "est ..." when live)
//  4. "" at width <= 20
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
	// No delta arrived in this turn: TTFT is not yet determined.
	if m.streamFirstDeltaAt.IsZero() {
		return ""
	}

	// Provider TTFT measures latency from the start of the current provider
	// turn (TurnStart) to the first streaming chunk of this turn. If TurnStart
	// was omitted (e.g. legacy/test callers), falls back to reqStartedAt.
	origin := m.streamStartedAt
	if origin.IsZero() {
		origin = m.reqStartedAt
	}
	ttft := m.streamFirstDeltaAt.Sub(origin)
	if ttft < 0 {
		ttft = 0
	}
	ttftStr := formatDuration(ttft)

	var (
		rateStr string
		tokStr  string
	)

	if !m.streamLastDeltaAt.IsZero() {
		streamElapsed := m.streamLastDeltaAt.Sub(m.streamFirstDeltaAt)
		if streamElapsed > 0 {
			if m.turnCompletionTokens > 0 {
				// Measured final rate for the current provider turn:
				// excludes the first token from numerator ((tokens - 1) / duration)
				// because it defines the start boundary of the streaming window.
				if m.turnCompletionTokens > 1 {
					rateTok := float64(m.turnCompletionTokens-1) / streamElapsed.Seconds()
					rateStr = fmt.Sprintf("%.1f tok/s", rateTok)
				}
				// Single token: rate is mathematically undefined — do not fabricate 0.0 tok/s.
				tokStr = fmt.Sprintf("%d tok", m.turnCompletionTokens)
			} else {
				// Live heuristic estimate for current stream: streamedChars / 4.
				if m.cachedLiveRate != "" && m.cachedLiveTok != "" {
					rateStr = m.cachedLiveRate
					tokStr = m.cachedLiveTok
				} else {
					rateStr, tokStr = formatLiveEstimate(m.streamedChars, streamElapsed)
				}
			}
		}
	}

	// Candidate ladder: drops tokens first, then TTFT, prioritizing rate retention.
	// Later entries with tokStr are only reached in single-token turns where rateStr == "".
	var candidates []string
	if rateStr != "" && tokStr != "" {
		candidates = append(candidates, fmt.Sprintf("TTFT %s · %s · %s", ttftStr, rateStr, tokStr))
	}
	if rateStr != "" {
		candidates = append(candidates, fmt.Sprintf("TTFT %s · %s", ttftStr, rateStr))
		candidates = append(candidates, rateStr)
	}
	if tokStr != "" {
		candidates = append(candidates, fmt.Sprintf("TTFT %s · %s", ttftStr, tokStr))
	}
	candidates = append(candidates, fmt.Sprintf("TTFT %s", ttftStr))
	if tokStr != "" {
		candidates = append(candidates, tokStr)
	}

	if fit, ok := firstFit(candidates, width, nil); ok {
		return fit
	}
	return ""
}

// formatLiveEstimate computes and formats the live heuristic throughput from
// accumulated character count and streaming duration (chars / 4).
// Uses the 'est ' prefix because '~' is outside AllowedUISymbols.
// Note: cachedLiveRate is throttled to 200ms pulses whereas streamLastDeltaAt
// updates on every delta batch; combining a slightly older rate with current TTFT
// has a sub-500ms jitter that is visually imperceptible to users.
func formatLiveEstimate(chars int, d time.Duration) (rate, tok string) {
	if chars <= 0 || d <= 0 {
		return "", ""
	}
	estTok := float64(chars) / 4.0
	rateTok := estTok / d.Seconds()
	rate = fmt.Sprintf("est %.1f tok/s", rateTok)
	tok = fmt.Sprintf("est %d tok", int(estTok))
	return rate, tok
}

// formatDuration formats a duration as a human-friendly seconds string (e.g. 1.24s).
func formatDuration(d time.Duration) string {
	secs := d.Seconds()
	if secs < 0.01 {
		return "0.00s"
	}
	return fmt.Sprintf("%.2fs", secs)
}
