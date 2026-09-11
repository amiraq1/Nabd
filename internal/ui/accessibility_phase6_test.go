package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

func TestWidthModeBoundaries(t *testing.T) {
	cases := map[int]WidthMode{20: WidthNarrow, 39: WidthNarrow, 40: WidthCompact, 79: WidthCompact, 80: WidthWide, 120: WidthWide}
	for width, want := range cases {
		if got := widthMode(width); got != want {
			t.Fatalf("widthMode(%d)=%q want %q", width, got, want)
		}
	}
}

func TestSemanticThemeNoColorEmitsNoANSI(t *testing.T) {
	theme := newSemanticTheme(true)
	for _, got := range []string{theme.Success.Render("success"), theme.Error.Render("error"), theme.Warning.Render("permission"), theme.Running.Render("running")} {
		if ansi.Strip(got) != got {
			t.Fatalf("NO_COLOR emitted ANSI: %q", got)
		}
	}
}

func TestMixedArabicPathPreservesCopyableOrder(t *testing.T) {
	path := "src/ملف-README.go"
	got := formatCodeSpan(path)
	if !strings.Contains(got, path) || got != "`"+path+"`" {
		t.Fatalf("mixed path changed: %q", got)
	}
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		for _, line := range wrap(got, width) {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d: %q", width, line)
			}
		}
	}
}

func TestAdaptiveErrorCardWidths(t *testing.T) {
	card := presentation.NewErrorCard(agent.ErrProviderTemporary, "temporary provider failure", "")
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		for _, line := range renderErrorCard(card, width) {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d: %q", width, line)
			}
		}
	}
}

func TestNavigationHintAdaptsByWidth(t *testing.T) {
	if strings.Contains(navigationHint(20), "Enter expand") {
		t.Fatal("narrow hint kept secondary metadata")
	}
	if !strings.Contains(navigationHint(120), "Enter expand") {
		t.Fatal("wide hint omitted expansion help")
	}
}
