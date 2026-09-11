package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestReleaseGateSlashRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, cmd := range AllSlashCommands() {
		if cmd.Name == "" || cmd.Usage == "" || cmd.Description == "" {
			t.Fatalf("incomplete slash metadata: %#v", cmd)
		}
		if seen[cmd.Name] {
			t.Fatalf("duplicate slash command %q", cmd.Name)
		}
		seen[cmd.Name] = true
		if !strings.HasPrefix(cmd.Name, "/") || !strings.HasPrefix(cmd.Usage, cmd.Name) {
			t.Fatalf("invalid command/usage pair: %#v", cmd)
		}
	}
}

func TestReleaseGateHelpAndDiagnosticsWidths(t *testing.T) {
	f := NewFeed()
	f.BuildFromEvents(acceptanceEvents())
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		for label, output := range map[string]string{
			"help":        CommandHelp(width),
			"diagnostics": f.Diagnostics(width),
		} {
			for _, line := range strings.Split(output, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("%s width %d exceeded by %q", label, width, line)
				}
			}
		}
	}
}

func TestReleaseGateViewportMatrix(t *testing.T) {
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		for _, height := range []int{8, 12, 24, 40} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				f := NewFeed()
				f.Update(tea.WindowSizeMsg{Width: width, Height: height})
				f.BuildFromEvents(acceptanceEvents())
				view := ansi.Strip(f.View())
				lines := strings.Split(view, "\n")
				if len(lines) > height {
					t.Fatalf("rendered %d rows into height %d", len(lines), height)
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > width {
						t.Fatalf("rendered width %d into width %d: %q", ansi.StringWidth(line), width, line)
					}
				}
			})
		}
	}
}

func TestReleaseGateDiagnosticsIsDiscoverable(t *testing.T) {
	if !strings.Contains(navigationHint(80), "d") {
		t.Fatal("wide navigation help must expose diagnostics shortcut")
	}
}
