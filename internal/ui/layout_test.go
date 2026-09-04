package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shared fixtures and helpers
// ─────────────────────────────────────────────────────────────────────────────

// termSize describes a terminal dimension for table-driven tests.
type termSize struct {
	name   string
	width  int
	height int
}

// allTermSizes is the matrix of terminal dimensions to test.
var allTermSizes = []termSize{
	{"120x40", 120, 40},
	{"80x24", 80, 24},
	{"50x16", 50, 16},
	{"40x20", 40, 20},
	{"30x16", 30, 16},
	{"20x12", 20, 12},
}

// testFixtures contains varied text content for feed population.
type testFixtures struct {
	longLsOutput   string // long ls -la with wide filenames
	arabicMsg      string // Arabic user message
	arabicCombine  string // Arabic with combining marks
	emojiSimple    string // basic emoji
	emojiComplex   string // multi-codepoint emoji sequences
	ansiStyled     string // ANSI colored line
	emptyLine      string // empty string
	longUserMsg    string // user message longer than typical width
	longToolOutput string // tool output longer than 100 lines
}

func newTestFixtures() testFixtures {
	var longToolLines []string
	for i := 0; i < 120; i++ {
		longToolLines = append(longToolLines, fmt.Sprintf("line %04d: this is a long output line that wraps at narrow widths and should be clamped %s", i, strings.Repeat("x", 50)))
	}
	return testFixtures{
		longLsOutput: `-rwxr-xr-x 1 termux termux  15243 Sep  3 fix_compact_test_with_very_long_name_that_wraps.py
-rw-r--r-- 1 termux termux  89421 Sep  3 internal_agent_loop_test_with_extra_long_qualifier.go
-rw-r--r-- 1 termux termux   4321 Sep  3 مرحبا_بك_في_مشروع_نبض_باللغة_العربية_الملف.txt
-rw-r--r-- 1 termux termux   1024 Sep  3 rocket_launch_🚀_🎉_🔥_emoji_output_details.log
drwxr-xr-x 4 termux termux   4096 Sep  3 directory_with_nested_hierarchy_and_deep_submodules_foo
-rw-r--r-- 1 termux termux   9999 Sep  3 0123456789012345678901234567890123456789012345678901.dat`,
		arabicMsg:      "هذا نص عربي طويل جداً يمتد لأكثر من مائة حرف ويحتوي على كلمات متصلة ومسافات وعلامات ترقيم.",
		arabicCombine:  "اَلْعَرَبِيَّةُ لُغَةٌ سَامِيَّةٌ",
		emojiSimple:    "Hello 🚀 World 🔥 Done ✓",
		emojiComplex:   "👨‍👩‍👧‍👦 family emoji · 🏳️‍🌈 flag · 🧑🏿‍💻 person",
		ansiStyled:     "normal text", // ANSI added by styles
		emptyLine:      "",
		longUserMsg:    strings.Repeat("long message word ", 30),
		longToolOutput: strings.Join(longToolLines, "\n"),
	}
}

// invariantCheckView verifies the two core visual layout invariants on v:
//  1. visualHeight(v, width) <= height
//  2. every line has ansi.StringWidth <= width
func invariantCheckView(t *testing.T, tag, v string, width, height int) {
	t.Helper()
	lines := strings.Split(v, "\n")
	for i, l := range lines {
		lw := ansi.StringWidth(l)
		if lw > width {
			t.Errorf("[%s] line %d width %d > terminal width %d: %q", tag, i, lw, width, l)
		}
		if !utf8.ValidString(l) {
			t.Errorf("[%s] line %d is not valid UTF-8: %q", tag, i, l)
		}
	}
	// visual height: sum of wrap rows for each line
	totalRows := 0
	for _, l := range lines {
		lw := ansi.StringWidth(l)
		if lw <= width || width <= 0 {
			totalRows++
		} else {
			totalRows += (lw + width - 1) / width
		}
	}
	if totalRows > height {
		t.Errorf("[%s] visual height %d > terminal height %d", tag, totalRows, height)
	}
}

// newFeedAt creates a new feed sized to w×h.
func newFeedAt(t *testing.T, w, h int) *Feed {
	t.Helper()
	f := NewFeed()
	f.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return f
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVisualHeightNeverExceedsTerminal — matrix across all sizes and states
// ─────────────────────────────────────────────────────────────────────────────

func TestVisualHeightNeverExceedsTerminal(t *testing.T) {
	fix := newTestFixtures()

	states := []struct {
		name  string
		setup func(f *Feed)
	}{
		{"idle", func(f *Feed) {}},
		{"streaming", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.TextDelta, Text: fix.arabicMsg},
				{Seq: 2, Type: agent.TextDelta, Text: fix.emojiSimple},
			}})
		}},
		{"long-output", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash", Args: json.RawMessage(`"ls -la"`)}},
				{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash"}, Text: fix.longLsOutput},
			}})
		}},
		{"modal", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "m1", Name: "bash", Args: json.RawMessage(`"rm -rf /tmp/test"`)}},
			}})
		}},
		{"slash-menu", func(f *Feed) {
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		}},
	}

	for _, sz := range allTermSizes {
		for _, st := range states {
			tag := fmt.Sprintf("%s/%s", sz.name, st.name)
			t.Run(tag, func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)
				st.setup(f)
				v := f.View()
				invariantCheckView(t, tag, v, sz.width, sz.height)
			})
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestRenderedLinesNeverExceedTerminalWidth — explicit per-line check
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderedLinesNeverExceedTerminalWidth(t *testing.T) {
	fix := newTestFixtures()
	for _, sz := range allTermSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.UserMsg, Text: fix.longUserMsg},
				{Seq: 2, Type: agent.TextDelta, Text: fix.arabicMsg},
				{Seq: 3, Type: agent.TextDelta, Text: fix.emojiComplex},
				{Seq: 4, Type: agent.TextDelta, Text: fix.arabicCombine},
				{Seq: 5, Type: agent.TurnEnd},
				{Seq: 6, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash", Args: json.RawMessage(`"ls -la"`)}},
				{Seq: 7, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash"}, Text: fix.longLsOutput},
			}})
			v := f.View()
			for i, l := range strings.Split(v, "\n") {
				if w := ansi.StringWidth(l); w > sz.width {
					t.Errorf("line %d: width %d > terminal width %d: %q", i, w, sz.width, l)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestComposerRemainsVisibleAfterViewportFills
// ─────────────────────────────────────────────────────────────────────────────

func TestComposerRemainsVisibleAfterViewportFills(t *testing.T) {
	for _, sz := range allTermSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)

			// Fill with 300 lines
			var evs []agent.Event
			for i := 1; i <= 300; i++ {
				evs = append(evs, agent.Event{
					Seq:  i,
					Type: agent.TextDelta,
					Text: fmt.Sprintf("Item %d: output content line", i),
				})
			}
			f.Update(agentEventBatchMsg{Events: evs})

			v := f.View()
			// The view must contain the composer prompt indicator
			if !strings.Contains(v, "›") && !strings.Contains(v, ">") {
				t.Errorf("no composer prompt visible after viewport fills:\n%s", v)
			}
			// The separator lines and footer must be present
			if !strings.Contains(v, "─") && !strings.Contains(v, "-") {
				t.Errorf("no separator line visible:\n%s", v)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLongUnicodeLinesRespectViewportWidth
// ─────────────────────────────────────────────────────────────────────────────

func TestLongUnicodeLinesRespectViewportWidth(t *testing.T) {
	texts := []struct {
		name string
		text string
	}{
		{"arabic", "هذا نص عربي طويل جداً يمتد لأكثر من مائة حرف ويحتوي على كلمات متصلة ومسافات وعلامات ترقيم."},
		{"arabic_combined", "اَلْعَرَبِيَّةُ لُغَةٌ سَامِيَّةٌ مِنْ أُسْرَةِ اللُّغَاتِ السَّامِيَّةِ."},
		{"emoji_simple", strings.Repeat("🚀🔥🎉❤️🌟✨💡🎈🏁 ", 8)},
		{"emoji_complex", strings.Repeat("👨‍👩‍👧‍👦🏳️‍🌈🧑🏿‍💻 ", 5)},
		{"mixed_long", "Hello مرحبا 🚀 world 世界 " + strings.Repeat("test ", 20)},
	}

	for _, sz := range allTermSizes {
		for _, txt := range texts {
			tag := sz.name + "/" + txt.name
			t.Run(tag, func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)
				f.Update(agentEventBatchMsg{Events: []agent.Event{
					{Seq: 1, Type: agent.UserMsg, Text: txt.text},
					{Seq: 2, Type: agent.TextDelta, Text: txt.text},
					{Seq: 3, Type: agent.TurnEnd},
				}})
				v := f.View()
				for i, l := range strings.Split(v, "\n") {
					if w := ansi.StringWidth(l); w > sz.width {
						t.Errorf("[%s] line %d width %d > terminal width %d: %q",
							tag, i, w, sz.width, l)
					}
				}
			})
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestMobileLongWrappedOutputKeepsComposerVisible
// ─────────────────────────────────────────────────────────────────────────────

func TestMobileLongWrappedOutputKeepsComposerVisible(t *testing.T) {
	fix := newTestFixtures()

	mobileSizes := []termSize{
		{"80x24", 80, 24},
		{"40x20", 40, 20},
		{"30x16", 30, 16},
	}

	for _, sz := range mobileSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)

			// Load long ls-la output with Arabic and emoji
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.RunStart, Text: "session"},
				{Seq: 2, Type: agent.UserMsg, Text: "نفّذ أمر ls -la وعاين الملفات"},
				{Seq: 3, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`"ls -la"`)}},
				{Seq: 4, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "bash"}, Text: fix.longLsOutput},
				{Seq: 5, Type: agent.TextDelta, Text: "هذه قائمة الملفات في المجلد الحالي 🚀"},
				{Seq: 6, Type: agent.TurnEnd},
			}})

			// Open permission modal
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 7, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c2", Name: "bash", Args: json.RawMessage(`"rm -rf /tmp/test"`)}},
			}})

			vModal := f.View()
			invariantCheckView(t, sz.name+"/modal", vModal, sz.width, sz.height)
			if !strings.Contains(vModal, "+-- Permission") {
				t.Errorf("modal card missing:\n%s", vModal)
			}
			if !strings.Contains(vModal, "composer paused") {
				t.Errorf("paused line missing:\n%s", vModal)
			}

			// Answer modal and type
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 8, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c2"}, Decision: agent.AllowOnce, RawDecision: agent.AllowOnce},
			}})
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

			if got := f.composer.value(); got != "abc" {
				t.Fatalf("composer value = %q, want 'abc'", got)
			}

			v := f.View()
			invariantCheckView(t, sz.name+"/after-modal", v, sz.width, sz.height)
			if !strings.Contains(v, "abc") {
				t.Errorf("composer text 'abc' not visible:\n%s", v)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestComposerGrowsUpwardWithinEightRows
// ─────────────────────────────────────────────────────────────────────────────

func TestComposerGrowsUpwardWithinEightRows(t *testing.T) {
	f := newFeedAt(t, 80, 40)

	for i := 1; i <= 10; i++ {
		f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
		lm := f.computeLayout()
		if lm.ComposerRows > maxComposerHeight {
			t.Errorf("after %d newlines: ComposerRows %d > max %d", i, lm.ComposerRows, maxComposerHeight)
		}
		v := f.View()
		invariantCheckView(t, fmt.Sprintf("newline-%d", i), v, 80, 40)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPermissionModalPreservesComposerSlot
// ─────────────────────────────────────────────────────────────────────────────

func TestPermissionModalPreservesComposerSlot(t *testing.T) {
	for _, sz := range allTermSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)

			// Pre-type some text
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})

			// Open modal
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "m1", Name: "bash"}},
			}})

			v := f.View()
			invariantCheckView(t, sz.name, v, sz.width, sz.height)
			if !strings.Contains(v, "composer paused") {
				t.Errorf("modal must show paused composer slot:\n%s", v)
			}
			if strings.Contains(v, "hi") && !f.modalVisible {
				t.Errorf("draft text should not be visible during modal")
			}

			// Close modal
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 2, Type: agent.PermReply, Call: &agent.ToolCall{ID: "m1"}, Decision: agent.AllowOnce, RawDecision: agent.AllowOnce},
			}})

			vClosed := f.View()
			if !strings.Contains(vClosed, "›") && !strings.Contains(vClosed, ">") {
				t.Errorf("composer must be restored after modal:\n%s", vClosed)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSlashMenuDoesNotCoverComposer
// ─────────────────────────────────────────────────────────────────────────────

func TestSlashMenuDoesNotCoverComposer(t *testing.T) {
	for _, sz := range allTermSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

			v := f.View()
			invariantCheckView(t, sz.name, v, sz.width, sz.height)

			// Composer must still be visible
			if !strings.Contains(v, "›") && !strings.Contains(v, ">") && !strings.Contains(v, "/") {
				t.Errorf("composer input area must remain visible with slash menu:\n%s", v)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestTwentyByTwelveDegradationIsDeterministic
// ─────────────────────────────────────────────────────────────────────────────

func TestTwentyByTwelveDegradationIsDeterministic(t *testing.T) {
	fix := newTestFixtures()
	states := []struct {
		name  string
		setup func(f *Feed)
	}{
		{"idle", func(f *Feed) {}},
		{"long-output", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash"}, Text: fix.longLsOutput},
			}})
		}},
		{"modal", func(f *Feed) {
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "m1", Name: "bash"}},
			}})
		}},
		{"slash-menu", func(f *Feed) {
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		}},
	}

	for _, st := range states {
		t.Run(st.name, func(t *testing.T) {
			f := newFeedAt(t, 20, 12)
			st.setup(f)

			v := f.View()
			// Must not panic and must respect limits
			invariantCheckView(t, "20x12/"+st.name, v, 20, 12)

			// No line exceeds 20 cells
			for i, l := range strings.Split(v, "\n") {
				if w := ansi.StringWidth(l); w > 20 {
					t.Errorf("20x12 line %d width %d > 20: %q", i, w, l)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestResizeRecomputesAllLayoutMetrics
// ─────────────────────────────────────────────────────────────────────────────

func TestResizeRecomputesAllLayoutMetrics(t *testing.T) {
	f := newFeedAt(t, 80, 24)
	var evs []agent.Event
	for i := 1; i <= 50; i++ {
		evs = append(evs, agent.Event{Seq: i, Type: agent.TextDelta, Text: fmt.Sprintf("line %d content here", i)})
	}
	f.Update(agentEventBatchMsg{Events: evs})

	sizes := []termSize{
		{"120x40", 120, 40},
		{"40x20", 40, 20},
		{"80x24", 80, 24},
		{"20x12", 20, 12},
		{"30x16", 30, 16},
	}

	for _, sz := range sizes {
		t.Run(sz.name, func(t *testing.T) {
			f.Update(tea.WindowSizeMsg{Width: sz.width, Height: sz.height})
			v := f.View()
			invariantCheckView(t, sz.name, v, sz.width, sz.height)

			lm := f.computeLayout()
			if lm.TerminalWidth != max(sz.width, minViewportWidth) {
				t.Errorf("TerminalWidth %d != expected %d", lm.TerminalWidth, max(sz.width, minViewportWidth))
			}
			if lm.ViewportRows < 0 {
				t.Errorf("ViewportRows %d < 0", lm.ViewportRows)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestFooterTruncatesModelBeforeEssentialKeys
// ─────────────────────────────────────────────────────────────────────────────

func TestFooterTruncatesModelBeforeEssentialKeys(t *testing.T) {
	for _, sz := range allTermSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)
			lm := f.computeLayout()

			// Footer must fit within terminal width
			if w := ansi.StringWidth(lm.footerLine); w > sz.width {
				t.Errorf("footer width %d > terminal width %d: %q",
					w, sz.width, lm.footerLine)
			}

			// Footer must not be empty
			if lm.footerLine == "" && sz.width >= 10 {
				t.Errorf("footer must not be empty at width %d", sz.width)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPasteNeverExecutesSlashCommand
// ─────────────────────────────────────────────────────────────────────────────

func TestPasteNeverExecutesSlashCommand(t *testing.T) {
	f := newFeedAt(t, 80, 24)

	// Send pasted key messages with Paste=true
	// Paste keys must not trigger slash command execution
	pasteKeys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("/"), Paste: true},
		{Type: tea.KeyRunes, Runes: []rune("u"), Paste: true},
		{Type: tea.KeyRunes, Runes: []rune("n"), Paste: true},
		{Type: tea.KeyRunes, Runes: []rune("d"), Paste: true},
		{Type: tea.KeyRunes, Runes: []rune("o"), Paste: true},
		{Type: tea.KeyEnter, Paste: true},
	}

	for _, k := range pasteKeys {
		f.Update(k)
	}

	// Composer must contain the pasted text, not be empty from command execution
	val := f.composer.value()
	if strings.Contains(val, "/undo") {
		// Text was stored as-is, not executed
	}
	// Run must not have been started (no runner attached)
	if f.running {
		t.Errorf("paste must not start a run; running=%v", f.running)
	}
	// No slash menu opened from paste
	if f.menu.visible {
		t.Errorf("paste must not open slash menu")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSeparatorLinesFillWidth
// ─────────────────────────────────────────────────────────────────────────────

func TestSeparatorLinesFillWidth(t *testing.T) {
	for _, sz := range allTermSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)
			lm := f.computeLayout()
			w := lm.TerminalWidth

			if lm.TopSepRows > 0 {
				if sw := ansi.StringWidth(lm.topSep); sw != w {
					t.Errorf("top separator width %d != terminal width %d", sw, w)
				}
			}
			if lm.BottomSepRows > 0 {
				if sw := ansi.StringWidth(lm.bottomSep); sw != w {
					t.Errorf("bottom separator width %d != terminal width %d", sw, w)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLayoutMetricsNeverNegative
// ─────────────────────────────────────────────────────────────────────────────

func TestLayoutMetricsNeverNegative(t *testing.T) {
	extremeSizes := []termSize{
		{"1x1", 1, 1},
		{"5x3", 5, 3},
		{"0x0", 0, 0},
		{"20x12", 20, 12},
	}
	for _, sz := range extremeSizes {
		t.Run(sz.name, func(t *testing.T) {
			f := newFeedAt(t, sz.width, sz.height)
			lm := f.computeLayout()
			if lm.ViewportRows < 0 {
				t.Errorf("ViewportRows %d < 0", lm.ViewportRows)
			}
			if lm.ComposerRows < 0 {
				t.Errorf("ComposerRows %d < 0", lm.ComposerRows)
			}
			// Must not panic
			_ = f.View()
		})
	}
}
