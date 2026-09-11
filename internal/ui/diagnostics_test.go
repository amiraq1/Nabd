package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"

	"github.com/charmbracelet/x/ansi"
)

func TestDiagnosticsIsWidthBoundedAndPrivate(t *testing.T) {
	f := NewFeed()
	f.BuildFromEvents([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "secret-prompt-value"},
		{Seq: 2, Type: agent.TextDelta, Text: "secret-output-value"},
	})
	f.addDiagnostic("secret-diagnostic-value")
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		got := f.Diagnostics(width)
		for _, secret := range []string{"secret-prompt-value", "secret-output-value", "secret-diagnostic-value"} {
			if strings.Contains(got, secret) {
				t.Fatalf("diagnostics leaked %q: %s", secret, got)
			}
		}
		for _, line := range strings.Split(got, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d exceeded by %q", width, line)
			}
		}
	}
}

func TestDiagnosticsSnapshotIsPure(t *testing.T) {
	f := NewFeed()
	f.BuildFromEvents([]agent.Event{{Seq: 1, Type: agent.UserMsg, Text: "hello"}})
	beforeLines := strings.Join(f.lines, "\n")
	beforeNotices := len(f.notices)
	first := f.Diagnostics(80)
	second := f.Diagnostics(80)
	if first != second {
		t.Fatalf("diagnostics changed without state mutation:\n%s\n%s", first, second)
	}
	if strings.Join(f.lines, "\n") != beforeLines || len(f.notices) != beforeNotices {
		t.Fatal("diagnostics mutated feed state")
	}
}

func TestDiagSlashCommandRunsLocally(t *testing.T) {
	cmd, ok := LookupSlashCommand("/diag")
	if !ok || cmd.HasArg {
		t.Fatalf("/diag registry entry missing or invalid: %#v", cmd)
	}
	f := NewFeed()
	f.composer.setValue("/diag")
	f.runCommand("/diag")
	if !f.composer.isEmpty() {
		t.Fatal("/diag did not clear composer")
	}
	if len(f.notices) == 0 || !strings.Contains(f.notices[len(f.notices)-1].Text, "Diagnostics") {
		t.Fatal("/diag did not add diagnostics dashboard")
	}
}
