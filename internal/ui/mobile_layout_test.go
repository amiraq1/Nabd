package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// visualHeightOf returns the count of visual rows occupied by s when printed
// to a terminal of width w, accounting for terminal auto-wrapping if any line
// exceeds w.
func visualHeightOf(s string, width int) int {
	if s == "" || width <= 0 {
		return 0
	}
	lines := strings.Split(s, "\n")
	total := 0
	for _, l := range lines {
		w := ansi.StringWidth(l)
		if w <= width {
			total++
		} else {
			total += (w + width - 1) / width
		}
	}
	return total
}

// TestMobileLongWrappedOutputKeepsComposerVisible reproduces the mobile Termux condition:
// a 40x20 terminal with long tool output (ls -la with long filenames), Arabic, and emoji.
// It verifies that visual height never exceeds 20, composer remains on-screen, and typing
// 'abc' is rendered stably.
func TestMobileLongWrappedOutputKeepsComposerVisible(t *testing.T) {
	sizes := []struct {
		width  int
		height int
		name   string
	}{
		{width: 80, height: 24, name: "80x24"},
		{width: 40, height: 20, name: "40x20"},
		{width: 30, height: 16, name: "30x16"},
	}

	for _, sz := range sizes {
		t.Run(sz.name, func(t *testing.T) {
			f, _ := feedWithRunner(t)
			f.Update(tea.WindowSizeMsg{Width: sz.width, Height: sz.height})

			// 1. Inject long ls -la output with long lines, Arabic, and emoji
			longLsOutput := `-rwxr-xr-x 1 termux termux  15243 Sep  3 19:40 fix_compact_test_with_very_long_name.py
-rw-r--r-- 1 termux termux  89421 Sep  3 19:41 internal_agent_loop_test_with_extra_long_qualifier.go
-rw-r--r-- 1 termux termux   4321 Sep  3 19:42 مرحبا_بك_في_مشروع_نبض_باللغة_العربية_الملف_الأول.txt
-rw-r--r-- 1 termux termux   1024 Sep  3 19:43 rocket_launch_emoji_test_🚀_🎉_🔥_output_details.log
drwxr-xr-x 4 termux termux   4096 Sep  3 19:44 directory_with_nested_hierarchy_and_deep_submodules
-rw-r--r-- 1 termux termux   9999 Sep  3 19:45 0123456789012345678901234567890123456789012345678901234567890123456789.dat`

			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.RunStart, Text: "session"},
				{Seq: 2, Type: agent.UserMsg, Text: "نفّذ أمر ls -la وعاين الملفات"},
				{Seq: 3, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`"ls -la"`)}},
				{Seq: 4, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "bash"}, Text: longLsOutput},
				{Seq: 5, Type: agent.TextDelta, Text: "هذه قائمة الملفات في المجلد الحالي 🚀"},
				{Seq: 6, Type: agent.TurnEnd},
			}})

			// 2. Open and test Permission Modal
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 7, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c2", Name: "bash", Args: json.RawMessage(`"rm -rf /tmp/test"`)}},
			}})

			vModal := f.View()
			// Assert visual height during modal <= terminal height
			if vh := visualHeightOf(vModal, sz.width); vh > sz.height {
				t.Fatalf("during modal: visual height %d exceeds terminal height %d:\n%s", vh, sz.height, vModal)
			}
			// Assert no line exceeds terminal width
			for i, line := range strings.Split(vModal, "\n") {
				if w := ansi.StringWidth(line); w > sz.width {
					t.Fatalf("during modal: line %d width %d exceeds terminal width %d: %q", i, w, sz.width, line)
				}
			}

			// 3. Close modal
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 8, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c2"}, Decision: agent.AllowOnce, EffectiveDecision: agent.AllowOnce},
			}})

			// 4. Type 'a', 'b', 'c' into composer
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

			if got := f.composer.value(); got != "abc" {
				t.Fatalf("composer value = %q, want 'abc'", got)
			}

			// 5. Check rendered view post-modal with typed input
			v := f.View()
			if vh := visualHeightOf(v, sz.width); vh > sz.height {
				t.Fatalf("visual height %d exceeds terminal height %d:\n%s", vh, sz.height, v)
			}
			for i, line := range strings.Split(v, "\n") {
				if w := ansi.StringWidth(line); w > sz.width {
					t.Fatalf("line %d width %d exceeds terminal width %d: %q", i, w, sz.width, line)
				}
			}

			// Assert composer is visible and contains 'abc'
			if !strings.Contains(v, "abc") {
				t.Fatalf("composer text 'abc' must be visible in View(), got:\n%s", v)
			}

			// 6. Test Slash Menu open and close
			f.composer.clear()
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
			vMenu := f.View()
			if vh := visualHeightOf(vMenu, sz.width); vh > sz.height {
				t.Fatalf("during slash menu: visual height %d exceeds terminal height %d:\n%s", vh, sz.height, vMenu)
			}
			// Close menu via Esc
			f.Update(tea.KeyMsg{Type: tea.KeyEscape})
			vMenuClosed := f.View()
			if !strings.Contains(vMenuClosed, "›") {
				t.Fatalf("composer prompt must remain visible after slash menu close:\n%s", vMenuClosed)
			}
		})
	}
}

// TestVisualHeightNeverExceedsTerminal verifies that visual height is <= height
// across an array of terminal dimensions.
func TestVisualHeightNeverExceedsTerminal(t *testing.T) {
	dimensions := []struct {
		width  int
		height int
	}{
		{width: 80, height: 24},
		{width: 50, height: 16},
		{width: 40, height: 20},
		{width: 30, height: 16},
		{width: 25, height: 10},
	}

	for _, d := range dimensions {
		f, _ := feedWithRunner(t)
		f.Update(tea.WindowSizeMsg{Width: d.width, Height: d.height})

		// Add 50 items with long lines
		var evs []agent.Event
		for i := 1; i <= 50; i++ {
			evs = append(evs, agent.Event{
				Seq:  i,
				Type: agent.TextDelta,
				Text: fmt.Sprintf("Line %d: %s\n", i, strings.Repeat("long-token-", 10)),
			})
		}
		f.Update(agentEventBatchMsg{Events: evs})

		v := f.View()
		vh := visualHeightOf(v, d.width)
		if vh > d.height {
			t.Errorf("dim %dx%d: visual height %d exceeds height %d", d.width, d.height, vh, d.height)
		}
		for i, line := range strings.Split(v, "\n") {
			if w := ansi.StringWidth(line); w > d.width {
				t.Errorf("dim %dx%d line %d: width %d exceeds terminal width %d", d.width, d.height, i, w, d.width)
			}
		}
	}
}

// TestComposerRemainsVisibleAfterViewportFills verifies that when the viewport
// overflows with hundreds of lines, the composer remains at the bottom of View().
func TestComposerRemainsVisibleAfterViewportFills(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.Update(tea.WindowSizeMsg{Width: 40, Height: 20})

	// Fill with 300 lines
	var evs []agent.Event
	for i := 1; i <= 300; i++ {
		evs = append(evs, agent.Event{
			Seq:  i,
			Type: agent.TextDelta,
			Text: fmt.Sprintf("Item %d: output content line\n", i),
		})
	}
	f.Update(agentEventBatchMsg{Events: evs})

	v := f.View()
	lines := strings.Split(v, "\n")
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "›") {
		t.Fatalf("last line of View() must be composer prompt, got: %q\nfull view:\n%s", lastLine, v)
	}
}

// TestLongUnicodeLinesRespectViewportWidth verifies that long Arabic text,
// emoji sequences, and mixed Unicode lines never exceed viewport width.
func TestLongUnicodeLinesRespectViewportWidth(t *testing.T) {
	widths := []int{30, 40, 50, 80}
	for _, w := range widths {
		f, _ := feedWithRunner(t)
		f.Update(tea.WindowSizeMsg{Width: w, Height: 24})

		f.Update(agentEventBatchMsg{Events: []agent.Event{
			{Seq: 1, Type: agent.UserMsg, Text: "هذا نص عربي طويل جدا يمتد لأكثر من مائة حرف ويحتوي على كلمات متصلة ومسافات وعلامات ترقيم."},
			{Seq: 2, Type: agent.TextDelta, Text: "مرحبا 🚀🔥🎉❤️🌟✨💡🎈🏁 هذا سطر مع إيموجي وكلمات طويلة متتالية."},
			{Seq: 3, Type: agent.TurnEnd},
		}})

		v := f.View()
		for i, line := range strings.Split(v, "\n") {
			if sw := ansi.StringWidth(line); sw > w {
				t.Fatalf("width %d line %d width %d exceeds limit: %q", w, i, sw, line)
			}
		}
	}
}
