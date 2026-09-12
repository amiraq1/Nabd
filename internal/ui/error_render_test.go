package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

func TestErrorCardNeverExceedsWidth(t *testing.T) {
	card := presentation.NewErrorCard(agent.ErrProviderTemporary, "Authorization: Bearer sk-secret and a very long failure message that must wrap or truncate safely", "")
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		for _, line := range renderErrorCard(card, width) {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width %d rendered %d: %q", width, got, line)
			}
			if strings.Contains(line, "sk-secret") {
				t.Fatalf("secret leaked at width %d: %q", width, line)
			}
		}
	}
}

func TestErrorCardWorksWithoutColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	joined := strings.Join(renderErrorCard(presentation.NewErrorCard(agent.ErrBudget, "limit", ""), 40), "\n")
	if !strings.Contains(joined, "!") || !strings.Contains(joined, "budget") {
		t.Fatalf("missing non-color semantics: %q", joined)
	}
}
