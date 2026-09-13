package presentation

import (
	"fmt"
	"strings"
	"time"
)

// RuntimeMeta is the small set of facts that answer "what is happening right
// now, and who is serving it": which turn, how many tokens the provider
// measured, how long the run has been going, and the route that actually
// committed. It carries no credentials and no stream identifiers.
type RuntimeMeta struct {
	Provider string
	Model    string
	Turn     int
	Tokens   int
	Elapsed  time.Duration
}

// Meta returns the runtime metadata for the status row. `now` is injected so
// elapsed time is testable and so the projector never reads the clock itself.
// Once a run has ended, elapsed freezes at the terminal event: a dead run must
// not keep counting.
func (p *StatusProjector) Meta(now time.Time) RuntimeMeta {
	m := RuntimeMeta{
		Provider: p.provider,
		Model:    p.model,
		Turn:     p.status.Turn,
		Tokens:   p.status.Usage.PromptTokens + p.status.Usage.CompletionTokens,
	}
	if !p.startedAt.IsZero() {
		end := p.endedAt
		if end.IsZero() {
			end = now
		}
		if d := end.Sub(p.startedAt); d > 0 {
			m.Elapsed = d
		}
	}
	return m
}

// RuntimeMetaVariants returns candidate status-row suffixes ordered from most
// to least informative. The caller picks the first one that fits its width,
// which is why this returns a list instead of one string: on a 40-column phone
// terminal a truncated "turn 3 · 6.6k tok · 12s · gr…" is worse than an intact
// "turn 3".
//
// Every field is passed through the display sanitizer with redaction enabled,
// because Provider and Model originate from provider responses.
func RuntimeMetaVariants(m RuntimeMeta) []string {
	turn := ""
	if m.Turn > 0 {
		turn = fmt.Sprintf("turn %d", m.Turn)
	}
	tokens := ""
	if m.Tokens > 0 {
		tokens = formatTokens(m.Tokens)
	}
	elapsed := ""
	if m.Elapsed > 0 {
		elapsed = formatElapsed(m.Elapsed)
	}
	provider := cleanField(m.Provider, "")
	model := cleanField(m.Model, "")
	route := provider
	if provider != "" && model != "" {
		route = provider + "/" + model
	}

	candidates := [][]string{
		{turn, tokens, elapsed, route},
		{turn, tokens, elapsed, provider},
		{turn, tokens, provider},
		{turn, elapsed, provider},
		{turn, provider},
		{turn, tokens},
		{turn},
	}

	out := make([]string, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, parts := range candidates {
		joined := joinParts(parts)
		if joined == "" || seen[joined] {
			continue
		}
		seen[joined] = true
		out = append(out, joined)
	}
	return out
}

func joinParts(parts []string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// formatTokens keeps the row short without inventing precision: exact below
// 1000, one decimal in thousands above it.
func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d tok", n)
	}
	s := fmt.Sprintf("%.1f", float64(n)/1000)
	s = strings.TrimSuffix(s, ".0")
	return s + "k tok"
}

// formatElapsed renders whole seconds below a minute, then m:ss.
func formatElapsed(d time.Duration) string {
	secs := int(d.Round(time.Second) / time.Second)
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}
