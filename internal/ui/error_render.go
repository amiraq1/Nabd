package ui

import (
	"fmt"
	"os"
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
	asciiOnly := os.Getenv("NABD_ASCII_ONLY") != ""
	noColor := os.Getenv("NO_COLOR") != ""

	tail := "…"
	mark := "✗ "
	if asciiOnly {
		tail = "..."
		mark = "x "
	}
	if card.Code == "budget" || card.Code == "max_turns" {
		mark = "! "
	}

	renderWarn := func(s string) string {
		if noColor {
			return s
		}
		return warn.Render(s)
	}
	renderBad := func(s string) string {
		if noColor {
			return s
		}
		return bad.Render(s)
	}
	renderDim := func(s string) string {
		if noColor {
			return s
		}
		return dim.Render(s)
	}

	line := func(prefix, value string) string {
		value = SanitizeForDisplay(value, DisplayPolicy{AllowNewline: false, Redact: true})
		value = strings.Join(strings.Fields(value), " ")
		return truncateToWidth(prefix+value, width, tail)
	}

	out := []string{renderBad(line(mark, card.Title)), renderDim(line("  code: ", string(card.Code)))}
	mode := widthMode(width)
	// The wait is the one number the reader acts on, so it is rendered at every
	// width — the width ladder may drop prose (Message) but never this.
	if card.WaitSeconds > 0 {
		out = append(out, renderWarn(line("  wait: ", fmt.Sprintf("%.0fs", card.WaitSeconds))))
	}
	// Persist paths are safety-critical at every width. Other diagnostic
	// details are progressively disclosed from compact mode upward.
	if card.JournalPath != "" {
		out = append(out, line("  journal: ", formatCodeSpan(card.JournalPath)))
	}
	if mode != WidthNarrow && card.Message != "" {
		out = append(out, line("  details: ", card.Message))
	}
	if card.Remedy != "" {
		remedyText := SanitizeForDisplay(card.Remedy, DisplayPolicy{AllowNewline: false, Redact: true})
		remedyText = strings.Join(strings.Fields(remedyText), " ")
		prefix := "  remedy: "
		indent := "  "
		if width < 30 {
			out = append(out, renderWarn(prefix))
			for _, wLine := range wrap(remedyText, width-len(indent)) {
				out = append(out, renderWarn(indent+wLine))
			}
		} else {
			avail := width - len(prefix)
			lines := wrap(remedyText, avail)
			if len(lines) > 0 {
				out = append(out, renderWarn(prefix+lines[0]))
				for _, wLine := range lines[1:] {
					out = append(out, renderWarn("    "+wLine))
				}
			}
		}
	}
	out = append(out, line("  action: ", card.ActionText))
	if card.RetryScope == presentation.RetryProviderTurn {
		if mode == WidthWide {
			out = append(out, renderDim(line("  safety: ", "retry does not approve or replay a tool")))
		}
		out = append(out, line("  ", "[r] retry  [d/Esc] close"))
	} else {
		out = append(out, line("  ", "[d/Esc] close"))
	}
	return out
}
