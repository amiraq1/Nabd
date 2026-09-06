package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var degradationSizes = []termSize{
	{"20x8", 20, 8},
	{"20x10", 20, 10},
	{"20x12", 20, 12},
	{"24x10", 24, 10},
	{"32x12", 32, 12},
	{"40x14", 40, 14},
	{"60x20", 60, 20},
	{"80x24", 80, 24},
}

type degradationScenario struct {
	name  string
	setup func(f *Feed)
}

func degradationScenarios() []degradationScenario {
	fix := newTestFixtures()
	return []degradationScenario{
		{"idle", func(f *Feed) {}},
		{"streaming", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.TextDelta, Text: fix.arabicMsg},
			}})
		}},
		{"tool-running", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash", Args: json.RawMessage(`"pwd"`)}},
			}})
		}},
		{"modal", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "m1", Name: "bash", Args: json.RawMessage(`"cat /etc/hosts"`)}},
			}})
		}},
		{"slash-menu", func(f *Feed) {
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		}},
		{"unseen", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.UserMsg, Text: "msg1"},
			}})
			f.follow = false
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 2, Type: agent.UserMsg, Text: "msg2"},
			}})
		}},
		{"long-tool-output", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash"}},
				{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash", Output: fix.longLsOutput, OK: true}},
			}})
		}},
	}
}

func TestViewIsIdempotentAndSideEffectFree(t *testing.T) {
	f := newFeedAt(t, 40, 14)
	// Mutate composer to a known width/height
	f.composer.resize(30, 3)
	cWidthBefore := f.composer.width
	cHeightBefore := f.composer.height

	v1 := f.View()
	cWidthAfter1 := f.composer.width
	cHeightAfter1 := f.composer.height

	if cWidthBefore != cWidthAfter1 || cHeightBefore != cHeightAfter1 {
		t.Fatalf("View() mutated composer state: width %d->%d, height %d->%d",
			cWidthBefore, cWidthAfter1, cHeightBefore, cHeightAfter1)
	}

	v2 := f.View()
	if v1 != v2 {
		t.Fatalf("View() is not idempotent: v1 != v2")
	}
}

func TestSmallScreenKeepsComposerAndFooter(t *testing.T) {
	for _, sz := range degradationSizes {
		for _, sc := range degradationScenarios() {
			t.Run(sz.name+"/"+sc.name, func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)
				sc.setup(f)
				v := f.View()
				lines := strings.Split(v, "\n")
				if len(lines) == 0 {
					t.Fatalf("View() returned empty string")
				}
				// Footer is the last line
				footer := lines[len(lines)-1]
				if strings.TrimSpace(footer) == "" {
					t.Fatalf("Footer line is blank: %q", footer)
				}
				// Composer or composer paused notice must be present above footer
				composerFound := false
				for _, l := range lines {
					if strings.Contains(l, "›") || strings.Contains(l, "composer paused") || strings.Contains(l, "permission") {
						composerFound = true
						break
					}
				}
				if !composerFound {
					t.Fatalf("Neither composer prompt nor paused notice found in output:\n%s", v)
				}
			})
		}
	}
}

func TestSmallScreenKeepsModalTitleAndSelectedChoice(t *testing.T) {
	// At small sizes with modal open, the modal title and choice must remain
	// visible. The default selection is Deny, so the narrow view must show
	// Deny as the selected choice — not Allow Once or Allow Session.
	for _, sz := range []termSize{{"20x10", 20, 10}, {"20x12", 20, 12}, {"24x10", 24, 10}} {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "m1", Name: "bash", Args: json.RawMessage(`"cat /etc/hosts"`)}},
			}})
			v := f.View()
			if !strings.Contains(v, "Permission") {
				t.Fatalf("[%s] modal title 'Permission' was cut from View():\n%s", sz.name, v)
			}
			// Strip ANSI before inspecting the selection marker.
			plain := ansi.Strip(v)
			if !strings.Contains(plain, "[*] Deny") {
				t.Fatalf("[%s] expected [*] Deny as the default selection, got:\n%s", sz.name, plain)
			}
			if strings.Contains(plain, "[*] Allow Once") || strings.Contains(plain, "[*] Allow Session") {
				t.Fatalf("[%s] [*] appears on a non-Deny choice (default should be Deny):\n%s", sz.name, plain)
			}
		})
	}
}

func TestSmallScreenKeepsRunningToolName(t *testing.T) {
	for _, sz := range []termSize{{"20x10", 20, 10}, {"20x12", 20, 12}} {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash", Args: json.RawMessage(`"sleep 10"`)}},
			}})
			v := f.View()
			if !strings.Contains(v, "bash") {
				t.Fatalf("[%s] tool name 'bash' must be visible:\n%s", sz.name, v)
			}
		})
	}
}

func TestDefensiveClampNeverTriggersAcrossTestedSizes(t *testing.T) {
	for _, sz := range degradationSizes {
		for _, sc := range degradationScenarios() {
			t.Run(sz.name+"/"+sc.name, func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)
				sc.setup(f)
				// We observe if computeLayout chrome exceeded terminal height
				lm := f.computeLayout()
				totalChrome := lm.HeaderRows + lm.RuntimeStatusRows + lm.TopSepRows +
					lm.ModalRows + lm.MenuRows + lm.UnseenRows + lm.ComposerRows +
					lm.BottomSepRows + lm.FooterRows
				if totalChrome > sz.height {
					t.Fatalf("[%s/%s] chrome %d exceeds terminal height %d — defensive clamp was triggered!",
						sz.name, sc.name, totalChrome, sz.height)
				}
			})
		}
	}
}

func TestDegradationRecomputesChromeAfterEachDrop(t *testing.T) {
	// In tight space, bottom sep and top sep must be dropped and ViewportRows recomputed accurately
	f := newFeedAt(t, 20, 10)
	lm := f.computeLayout()
	total := lm.HeaderRows + lm.RuntimeStatusRows + lm.TopSepRows +
		lm.ModalRows + lm.MenuRows + lm.UnseenRows + lm.ComposerRows +
		lm.BottomSepRows + lm.FooterRows + lm.ViewportRows
	if total > 10 {
		t.Fatalf("total layout rows %d > terminal height 10", total)
	}
}

func TestViewNeverExceedsTerminalHeight(t *testing.T) {
	for _, sz := range degradationSizes {
		for _, sc := range degradationScenarios() {
			t.Run(sz.name+"/"+sc.name, func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)
				sc.setup(f)
				v := f.View()
				lines := strings.Split(v, "\n")
				if len(lines) > sz.height {
					t.Fatalf("[%s/%s] line count %d > terminal height %d", sz.name, sc.name, len(lines), sz.height)
				}
			})
		}
	}
}

func TestViewNeverExceedsTerminalWidthInCells(t *testing.T) {
	for _, sz := range degradationSizes {
		for _, sc := range degradationScenarios() {
			t.Run(sz.name+"/"+sc.name, func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)
				sc.setup(f)
				v := f.View()
				for i, line := range strings.Split(v, "\n") {
					if w := ansi.StringWidth(line); w > sz.width {
						t.Fatalf("[%s/%s] line %d cell width %d > terminal width %d: %q", sz.name, sc.name, i, w, sz.width, line)
					}
				}
			})
		}
	}
}

func TestViewOutputIsValidUTF8(t *testing.T) {
	for _, sz := range degradationSizes {
		for _, sc := range degradationScenarios() {
			t.Run(sz.name+"/"+sc.name, func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)
				sc.setup(f)
				v := f.View()
				if !utf8.ValidString(v) {
					t.Fatalf("[%s/%s] output is not valid UTF-8", sz.name, sc.name)
				}
			})
		}
	}
}
