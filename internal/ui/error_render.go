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
	if card.Message != "" {
		out = append(out, line("  details: ", card.Message))
	}
	if card.JournalPath != "" {
		out = append(out, line("  journal: ", card.JournalPath))
	}
	out = append(out, line("  action: ", card.ActionText))
	if card.RetryScope == presentation.RetryProviderTurn {
		out = append(out, dim.Render(line("  safety: ", "retry does not approve or replay a tool")), line("  ", "[r] retry request  [d/Esc] close"))
	} else {
		out = append(out, line("  ", "[d/Esc] close"))
	}
	return out
}
