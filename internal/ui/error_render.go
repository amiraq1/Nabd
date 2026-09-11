package ui

import (
	"strings"

	"nabd/internal/presentation"
)

func renderErrorCard(card *presentation.ErrorCard, width int) []string {
	if card == nil {
		return nil
	}
	if width <= 0 {
		width = DefaultWidth
	}
	line := func(prefix, value string) string {
		value = SanitizeForDisplay(value, DisplayPolicy{AllowNewline: false, Redact: true})
		value = strings.Join(strings.Fields(value), " ")
		return truncateToWidth(prefix+value, width, "…")
	}
	mark := "✗ "
	if card.Code == "budget" || card.Code == "max_turns" {
		mark = "! "
	}
	out := []string{bad.Render(line(mark, card.Title)), dim.Render(line("  code: ", string(card.Code)))}
	mode := widthMode(width)
	// Persist paths are safety-critical at every width. Other diagnostic
	// details are progressively disclosed from compact mode upward.
	if card.JournalPath != "" {
		out = append(out, line("  journal: ", formatCodeSpan(card.JournalPath)))
	}
	if mode != WidthNarrow && card.Message != "" {
		out = append(out, line("  details: ", card.Message))
	}
	out = append(out, line("  action: ", card.ActionText))
	if card.RetryScope == presentation.RetryProviderTurn {
		if mode == WidthWide {
			out = append(out, dim.Render(line("  safety: ", "retry does not approve or replay a tool")))
		}
		out = append(out, line("  ", "[r] retry  [d/Esc] close"))
	} else {
		out = append(out, line("  ", "[d/Esc] close"))
	}
	return out
}
