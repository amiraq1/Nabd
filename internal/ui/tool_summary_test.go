package ui

import (
	"strings"
	"testing"

	"nabd/internal/presentation"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestToolSummaryNeverExceedsWidth(t *testing.T) {
	next := 170
	card := &presentation.ToolCard{Name: "read_file", Args: "internal/a/very/long/path/README.md", Status: presentation.ToolDone, Duration: 2400, Truncated: true, NextOffset: &next, OutputState: presentation.OutputTruncated}
	for _, width := range []int{20, 40, 80, 120} {
		line := renderToolSummary(card, width)
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("width %d rendered %d: %q", width, got, line)
		}
	}
}

func TestToolSummaryWorksWithoutColor(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	card := &presentation.ToolCard{Name: "bash", Args: "go test ./...", Status: presentation.ToolFailed, ExitCode: 1, Duration: 2400}
	plain := ansi.Strip(renderToolSummary(card, 80))
	if !strings.Contains(plain, "Bash") || !strings.Contains(plain, "exit 1") {
		t.Fatalf("summary is color-dependent: %q", plain)
	}
}

func TestToolFailureIsUnderstandableWithoutColor(t *testing.T) {
	for _, tc := range []struct {
		status presentation.ToolStatus
		want   string
	}{
		{presentation.ToolPending, "awaiting approval"},
		{presentation.ToolFailed, "failed"},
		{presentation.ToolDenied, "denied"},
		{presentation.ToolCancelled, "cancelled"},
		{presentation.ToolRunning, "running"},
	} {
		card := &presentation.ToolCard{Name: "bash", Status: tc.status}
		plain := ansi.Strip(renderToolSummary(card, 40))
		if !strings.Contains(plain, tc.want) {
			t.Errorf("status %s: %q", tc.status, plain)
		}
	}
}

func TestToolSummarySanitizesSubject(t *testing.T) {
	card := &presentation.ToolCard{Name: "bash", Args: "printf ok\nAuthorization: Bearer secret-token\x1b[2J", Status: presentation.ToolRunning}
	plain := ansi.Strip(renderToolSummary(card, 120))
	if strings.Contains(plain, "\n") || strings.Contains(plain, "\x1b") || strings.Contains(plain, "secret-token") {
		t.Fatalf("unsafe summary: %q", plain)
	}
}

func TestExpandedReadShowsNextOffsetAndSavedState(t *testing.T) {
	next := 170
	it := presentation.FeedItem{Type: presentation.ItemTool, Tool: &presentation.ToolCard{Name: "read_file", Args: "README.md", Status: presentation.ToolDone, Output: "132|hello", OutputState: presentation.OutputSaved, Truncated: true, NextOffset: &next}}
	plain := ansi.Strip(strings.Join(renderTool(it, 80, true), "\n"))
	if !strings.Contains(plain, "next: offset=170") || !strings.Contains(plain, "132 | hello") {
		t.Fatalf("expanded read missing metadata/output: %q", plain)
	}
}

func TestExpandedUnavailableOutputDoesNotPromiseFullOutput(t *testing.T) {
	it := presentation.FeedItem{Type: presentation.ItemTool, Tool: &presentation.ToolCard{Name: "read_file", Args: "README.md", Status: presentation.ToolDone, OutputState: presentation.OutputUnavailable, Truncated: true}}
	plain := strings.ToLower(ansi.Strip(strings.Join(renderTool(it, 80, true), "\n")))
	if !strings.Contains(plain, "output: unavailable") || strings.Contains(plain, "full output") {
		t.Fatalf("misleading unavailable state: %q", plain)
	}
}

func TestHugeToolOutputRemainsBounded(t *testing.T) {
	it := presentation.FeedItem{Type: presentation.ItemTool, Tool: &presentation.ToolCard{Name: "bash", Status: presentation.ToolDone, Output: strings.Repeat("0123456789\n", 10000), OutputState: presentation.OutputSaved}}
	lines := renderTool(it, 40, true)
	if len(lines) > maxToolOutputLines+10 {
		t.Fatalf("huge output rendered %d lines", len(lines))
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("overflow: %q", line)
		}
	}
}

// TestToolStatusSymbolsAndASCIIFallback verifies UI-F3:
// 1. Table-driven expectation for Unicode and ASCII symbols across all statuses.
// 2. All status symbols occupy exactly 1 terminal cell according to ansi.StringWidth.
// 3. No non-ASCII runes (>= 128) leak in ASCII mode.
func TestToolStatusSymbolsAndASCIIFallback(t *testing.T) {
	cases := []struct {
		name      string
		status    presentation.ToolStatus
		wantUni   string
		wantASCII string
	}{
		{name: "Pending", status: presentation.ToolPending, wantUni: "○", wantASCII: "o"},
		{name: "Running", status: presentation.ToolRunning, wantUni: "●", wantASCII: ">"},
		{name: "Done", status: presentation.ToolDone, wantUni: "✓", wantASCII: "+"},
		{name: "Failed", status: presentation.ToolFailed, wantUni: "✗", wantASCII: "x"},
		{name: "Denied", status: presentation.ToolDenied, wantUni: "✗", wantASCII: "x"},
		{name: "Cancelled", status: presentation.ToolCancelled, wantUni: "✗", wantASCII: "x"},
		{name: "unknown/default", status: presentation.ToolStatus("unknown_status"), wantUni: "·", wantASCII: "."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Unicode mode
			t.Setenv("NABD_ASCII_ONLY", "")
			uRaw := toolStatusSymbol(tc.status)
			uStripped := ansi.Strip(uRaw)
			if uStripped != tc.wantUni {
				t.Errorf("%s Unicode symbol = %q, want %q", tc.name, uStripped, tc.wantUni)
			}
			if gotW := ansi.StringWidth(uStripped); gotW != 1 {
				t.Errorf("%s Unicode width = %d, want 1", tc.name, gotW)
			}

			// 2. ASCII mode
			t.Setenv("NABD_ASCII_ONLY", "1")
			aRaw := toolStatusSymbol(tc.status)
			aStripped := ansi.Strip(aRaw)
			if aStripped != tc.wantASCII {
				t.Errorf("%s ASCII symbol = %q, want %q", tc.name, aStripped, tc.wantASCII)
			}
			if gotW := ansi.StringWidth(aStripped); gotW != 1 {
				t.Errorf("%s ASCII width = %d, want 1", tc.name, gotW)
			}
			for _, r := range aStripped {
				if r >= 128 {
					t.Errorf("%s ASCII symbol %q contains non-ASCII rune %q (U+%04X)", tc.name, aStripped, r, r)
				}
			}
		})
	}
}
