package ui

// rtl_shape_prose_test.go — Step 1 (shape-after-Markdown) test suite.
//
// Roles:
//   - TestL1_* drive shapeProseRuns directly on parseInlineSemantic output
//     (visual string in logical order plus rebuilt spans).
//   - TestL2_* drive renderAssistant under NABD_RTL=reorder (visual order,
//     ANSI stripped; SGR runs where style matters). The fail-first baseline
//     for this level is captured in
//     docs/reports/raw/run-20261011-1252-rtl-step1-failfirst.txt.
//   - TestL2_Protection_* pass on both the pre-Step-1 and Step-1 pipelines by
//     design; they guard against regressions rather than proving the move.
//   - TestL2_Characterization_* pin existing rtl.Layout behaviour that this
//     step relies on but does not decide (memo C1/D2); the long-bold-run
//     wrap fallback is pre-existing debt, tracked separately.
//   - TestShapeProseRunsInvariants and FuzzShapeProseRuns hold the rebuilding
//     invariants (ordered, bounded, non-overlapping, literal island bytes).
//
// Golden values are hand-computed from the joining rules and match
// docs/rtl-step1-shape-after-markdown.md. Presentation forms used here are
// exactly the ones blessed there: FEDB, FE98, FE90, FE95, FE8F, FE8E, FEFB,
// and U+064E.

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"nabd/internal/presentation"
	"nabd/internal/rtl"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// Presentation forms used by the golden cases.
const (
	formBehIsolated = "\uFE8F"
	formBehFinal    = "\uFE90"
	formTehIsolated = "\uFE95"
	formTehMedial   = "\uFE98"
	formKafInitial  = "\uFEDB"
	formAlefFinal   = "\uFE8E"
	formLamAlef     = "\uFEFB"
	markFatha       = "\u064E"
)

// shapedForms is every presentation form used in this file; tests use it to
// assert that a path did NOT shape anything.
const shapedForms = "\uFE8F\uFE90\uFE95\uFE98\uFEDB\uFE8E\uFEFB"

// ── helpers ─────────────────────────────────────────────────────────────

// pinRTLReorder pins NABD_RTL=reorder for one test and restores the
// process-wide mode cache afterwards, so no display mode leaks into other
// tests in the package.
func pinRTLReorder(t *testing.T) {
	t.Helper()
	t.Setenv("NABD_ASCII_ONLY", "")
	t.Setenv("NABD_RTL", "reorder")
	resetRTLModeCache()
	t.Cleanup(resetRTLModeCache)
}

// pinColorProfile forces a colour profile so lipgloss emits SGR sequences in
// a non-TTY test process, and neutralises NO_COLOR/TERM so environment
// detection cannot silently disable styling. The previous profile is
// restored in Cleanup.
func pinColorProfile(t *testing.T) {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "")
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// sgrRun is a maximal run of text sharing one bold state.
type sgrRun struct {
	text string
	bold bool
}

// sgrRuns splits s into runs by SGR bold state. It is a state machine: bold
// is tracked across 1/22/0 and extended-colour parameters (38/48/58) are
// skipped so colour indexes are never read as attribute numbers. It works
// without a TTY.
func sgrRuns(s string) []sgrRun {
	var runs []sgrRun
	bold := false
	add := func(text string) {
		if text == "" {
			return
		}
		if n := len(runs); n > 0 && runs[n-1].bold == bold {
			runs[n-1].text += text
			return
		}
		runs = append(runs, sgrRun{text: text, bold: bold})
	}
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j == len(s) {
				add(s[i:])
				break
			}
			if s[j] == 'm' {
				bold = applySGRBold(s[i+2:j], bold)
			}
			i = j + 1
			continue
		}
		j := strings.IndexByte(s[i:], '\x1b')
		if j < 0 {
			add(s[i:])
			break
		}
		if j == 0 {
			add(s[i : i+1]) // lone ESC outside a CSI: literal text
			i++
			continue
		}
		add(s[i : i+j])
		i += j
	}
	return runs
}

// applySGRBold applies one SGR parameter list to the bold state.
func applySGRBold(params string, bold bool) bool {
	if params == "" {
		return false // ESC[m is a full reset
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "0":
			bold = false
		case "1":
			bold = true
		case "22":
			bold = false
		case "38", "48", "58":
			// Extended colour: skip its parameters.
			switch {
			case i+1 < len(parts) && parts[i+1] == "5":
				i += 2
			case i+1 < len(parts) && parts[i+1] == "2":
				i += 4
			}
		}
	}
	return bold
}

// cps renders a string as space-separated codepoints for failure messages.
func cps(s string) string {
	var b strings.Builder
	for _, r := range s {
		fmt.Fprintf(&b, "U+%04X ", r)
	}
	return strings.TrimSpace(b.String())
}

// assertSpans compares span slices field by field, reporting every diff.
func assertSpans(t *testing.T, got, want []rtl.Span) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("rebuilt spans: got %d, want %d\n got: %+v\nwant: %+v",
			len(got), len(want), got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("rebuilt span %d = %+v, want %+v\n got: %+v\nwant: %+v",
				i, got[i], want[i], got, want)
		}
	}
}

// checkedShape asserts both halves of an L1 golden: the visual string and
// the rebuilt spans. Both diffs are reported, then the test ends.
func checkedShape(t *testing.T, visual, wantVisual string, spans, wantSpans []rtl.Span) {
	t.Helper()
	if visual != wantVisual {
		t.Errorf("visual = %q (%s)\n      want %q (%s)",
			visual, cps(visual), wantVisual, cps(wantVisual))
	}
	assertSpans(t, spans, wantSpans)
}

// assertRuns compares SGR runs field by field.
func assertRuns(t *testing.T, got, want []sgrRun) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("SGR runs: got %d, want %d\n got: %+v\nwant: %+v",
			len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("SGR run %d = %+v, want %+v\n got: %+v\nwant: %+v",
				i, got[i], want[i], got, want)
		}
	}
}

// shapeMarkdownInline runs the Step 1 front half on Markdown text: parse
// inline syntax, then shape the prose runs.
func shapeMarkdownInline(t *testing.T, text string, style uint16) (string, []rtl.Span) {
	t.Helper()
	logical, spans := parseInlineSemantic(text, style)
	return shapeProseRuns(logical, spans)
}

// renderAssistantBody renders an assistant item and returns the body without
// the "Nabd" role header line.
func renderAssistantBody(t *testing.T, text string, width int) string {
	t.Helper()
	it := presentation.FeedItem{Type: presentation.ItemAssistant, Text: text}
	lines := renderAssistant(it, width)
	if len(lines) < 2 {
		t.Fatalf("renderAssistant(%q, %d) = %q, want header plus body", text, width, lines)
	}
	return strings.Join(lines[1:], "\n")
}

// ── test tooling ────────────────────────────────────────────────────────

// TestSGRRunsTracksBoldState is the control for the L2 style assertions: it
// proves sgrRuns can observe bold in exactly the shapes lipgloss emits,
// including plain text immediately before a bold sequence (a case that must
// not swallow the sequence).
func TestSGRRunsTracksBoldState(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []sgrRun
	}{
		{"plain", "abc", []sgrRun{{"abc", false}}},
		{"bold", "\x1b[1ma\x1b[0m", []sgrRun{{"a", true}}},
		{"plain then bold", "x\x1b[1my\x1b[0m", []sgrRun{{"x", false}, {"y", true}}},
		{"bold then plain via 22", "\x1b[1ma\x1b[22mb", []sgrRun{{"a", true}, {"b", false}}},
		{"bold then plain via 0", "\x1b[1ma\x1b[0mb", []sgrRun{{"a", true}, {"b", false}}},
		{"combined 1;38;5", "\x1b[1;38;5;204ma\x1b[0m", []sgrRun{{"a", true}}},
		{"colour index one is not bold", "\x1b[38;5;1ma\x1b[0m", []sgrRun{{"a", false}}},
		{"truecolour params are skipped", "\x1b[38;2;1;2;3ma\x1b[0m", []sgrRun{{"a", false}}},
		{"reset then reopen merges", "\x1b[1ma\x1b[0m\x1b[1mb\x1b[0m", []sgrRun{{"ab", true}}},
		{"empty parameter reset", "\x1b[1ma\x1b[mb", []sgrRun{{"a", true}, {"b", false}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRuns(t, sgrRuns(tc.in), tc.want)
		})
	}
}

// ── L1: shapeProseRuns on parser output ─────────────────────────────────

func TestL1_CodeSpanStaysLiteral(t *testing.T) {
	// Invariant case: a code span is never shaped, so the visual string
	// equals the logical input.
	logical, spans := parseInlineSemantic("`مرحبا`", styleNone)
	visual, out := shapeProseRuns(logical, spans)
	checkedShape(t, visual, "`مرحبا`", out,
		[]rtl.Span{{Start: 0, End: 12, Kind: rtl.Code, StyleID: styleNone}})
}

func TestL1_CodeInsideBoldKeepsStyle(t *testing.T) {
	// Rebuilt islands preserve their original StyleID (memo implementation
	// note): code inside bold stays bold in visual-offset space.
	logical, spans := parseInlineSemantic("**`x`**", styleNone)
	if logical != "`x`" {
		t.Fatalf("parser logical = %q, want %q", logical, "`x`")
	}
	visual, out := shapeProseRuns(logical, spans)
	checkedShape(t, visual, "`x`", out,
		[]rtl.Span{{Start: 0, End: 3, Kind: rtl.Code, StyleID: styleBold}})
}

func TestL1_BoldBoundaryJoinsWord(t *testing.T) {
	// `**كت**ب`: the style boundary between ت and ب must not break joining.
	logical, spans := parseInlineSemantic("**كت**ب", styleNone)
	if logical != "كتب" {
		t.Fatalf("parser logical = %q (%s), want %q", logical, cps(logical), "كتب")
	}
	visual, out := shapeProseRuns(logical, spans)
	checkedShape(t, visual, formKafInitial+formTehMedial+formBehFinal, out,
		// Plain prose clusters stay gaps, so only the bold run is rebuilt as
		// a span (memo decision D2).
		[]rtl.Span{{Start: 0, End: 6, Kind: rtl.Prose, StyleID: styleBold}})
}

func TestL1_LamAlefTakesBoldLamStyle(t *testing.T) {
	// `**ل**ا`: the ligature is one cluster and takes the Lam's style (bold).
	visual, out := shapeMarkdownInline(t, "**ل**ا", styleNone)
	checkedShape(t, visual, formLamAlef, out,
		[]rtl.Span{{Start: 0, End: 3, Kind: rtl.Prose, StyleID: styleBold}})
}

func TestL1_LamAlefTakesPlainLamStyle(t *testing.T) {
	// `ل**ا**`: the ligature takes the FIRST char's style (plain), so the
	// bold on ا is absorbed and no styled span remains.
	visual, out := shapeMarkdownInline(t, "ل**ا**", styleNone)
	checkedShape(t, visual, formLamAlef, out, nil)
}

func TestL1_CodeBreaksProseJoining(t *testing.T) {
	logical, spans := parseInlineSemantic("ب`ت`ت", styleNone)
	visual, out := shapeProseRuns(logical, spans)
	checkedShape(t, visual, formBehIsolated+"`ت`"+formTehIsolated, out,
		// The code span moves from logical [2,6) to visual [3,7): FE8F is
		// three bytes, and the code bytes stay literal.
		[]rtl.Span{{Start: 3, End: 7, Kind: rtl.Code, StyleID: styleNone}})
}

func TestL1_DiacriticsFollowTheirLetter(t *testing.T) {
	// `**كَ**تَبَ`: transparent marks stay U+064E and move with their base.
	visual, out := shapeMarkdownInline(t, "**كَ**تَبَ", styleNone)
	want := formKafInitial + markFatha + formTehMedial + markFatha + formBehFinal + markFatha
	checkedShape(t, visual, want, out,
		[]rtl.Span{{Start: 0, End: 5, Kind: rtl.Prose, StyleID: styleBold}})
}

func TestL1_UnclosedBoldIsLiteralProse(t *testing.T) {
	// `**كتاب` with no closer: `**` stays literal and the whole line is one
	// prose run, shaped jointly.
	visual, out := shapeMarkdownInline(t, "**كتاب", styleNone)
	want := "**" + formKafInitial + formTehMedial + formAlefFinal + formBehIsolated
	checkedShape(t, visual, want, out, nil)
}

func TestL1_ListContentShapes(t *testing.T) {
	prefix, _, content, ok := parseList("- كتاب")
	if !ok || prefix != "- " {
		t.Fatalf("parseList(- كتاب) = %q, %q, %v", prefix, content, ok)
	}
	logical, spans := parseInlineSemantic(content, styleNone)
	visual, out := shapeProseRuns(logical, spans)
	want := formKafInitial + formTehMedial + formAlefFinal + formBehIsolated
	checkedShape(t, visual, want, out, nil)
}

func TestL1_CodeAfterLamAlefShrinksOffsets(t *testing.T) {
	logical, spans := parseInlineSemantic("لا`x`", styleNone)
	visual, out := shapeProseRuns(logical, spans)
	checkedShape(t, visual, formLamAlef+"`x`", out,
		// Logical code span [4,7); the ligature shrinks the prose from 4 to
		// 3 bytes, so the visual span is [3,6).
		[]rtl.Span{{Start: 3, End: 6, Kind: rtl.Code, StyleID: styleNone}})
}

func TestL1_CodeAfterArabicShiftsOffsets(t *testing.T) {
	logical, spans := parseInlineSemantic("كتاب `x`", styleNone)
	visual, out := shapeProseRuns(logical, spans)
	want := formKafInitial + formTehMedial + formAlefFinal + formBehIsolated + " `x`"
	checkedShape(t, visual, want, out,
		// Logical code span [9,12); each shaped letter grows 2→3 bytes, so
		// the visual span is [13,16) and the literal bytes are unchanged.
		[]rtl.Span{{Start: 13, End: 16, Kind: rtl.Code, StyleID: styleNone}})
}

func TestL1_URLInProsePassesThrough(t *testing.T) {
	logical, spans := parseInlineSemantic("كتب https://example.com", styleNone)
	visual, out := shapeProseRuns(logical, spans)
	want := formKafInitial + formTehMedial + formBehFinal + " https://example.com"
	// URL/Path/Command are reserved kinds and are not emitted today, so the
	// URL is prose and must survive byte-for-byte.
	checkedShape(t, visual, want, out, nil)
}

func TestL1_URLSpanIsLiteralIsland(t *testing.T) {
	// If a URL span is ever emitted (reserved kind), it is an island exactly
	// like Code: bytes verbatim, kind and style survive, offsets shift with
	// the prose before it.
	logical := "كتب https://example.com"
	uStart := strings.Index(logical, "https")
	visual, out := shapeProseRuns(logical,
		[]rtl.Span{{Start: uStart, End: len(logical), Kind: rtl.URL, StyleID: styleNone}})
	want := formKafInitial + formTehMedial + formBehFinal + " https://example.com"
	checkedShape(t, visual, want, out,
		[]rtl.Span{{Start: 10, End: 29, Kind: rtl.URL, StyleID: styleNone}})
}

func TestL1_NonArabicPassthrough(t *testing.T) {
	// Probe D (docs/reports/raw/run-20261011-1317-rtl-step1-d2wrap-probe.txt):
	// the engine leaves Latin, digits and emoji unchanged.
	for _, in := range []string{"hello café", "123", "\U0001F468\u200D\U0001F469\u200D\U0001F467"} {
		logical, spans := parseInlineSemantic(in, styleNone)
		visual, out := shapeProseRuns(logical, spans)
		if visual != in {
			t.Errorf("passthrough failed for %q: got %q (%s)", in, visual, cps(visual))
		}
		assertSpans(t, out, nil)
	}
}

func TestL1_PresentationFormsArePreserved(t *testing.T) {
	// Probe D: already-shaped text passes through unchanged, so re-shaping
	// cannot corrupt it — the gate the retired shapeArabicText idempotence
	// test held.
	in := formKafInitial + formTehMedial + formBehFinal
	logical, spans := parseInlineSemantic(in, styleNone)
	visual, out := shapeProseRuns(logical, spans)
	checkedShape(t, visual, in, out, nil)
}

func TestL1_ParserEmitsNoReservedSpanKinds(t *testing.T) {
	inputs := []string{
		"plain كتب",
		"**bold كتب**",
		"`code`",
		"!`cmd`",
		"https://example.com/x",
		"src/main.go",
	}
	for _, in := range inputs {
		_, spans := parseInlineSemantic(in, styleNone)
		for _, s := range spans {
			if s.Kind != rtl.Prose && s.Kind != rtl.Code {
				t.Fatalf("parseInlineSemantic(%q) emitted kind %d; URL/Path/Command are reserved (rtl/layout.go:48) and must not be emitted until PR 2",
					in, s.Kind)
			}
		}
	}
}

// ── L2: renderAssistant under NABD_RTL=reorder (visual order) ───────────

func TestL2_BoldStyleSurvives(t *testing.T) {
	// Guards the Step 1 reorder. Pre-Step-1 fail-first baseline (shaping
	// before Markdown): got "FE8F FE96 FEDB" — the `**` boundary broke the
	// ت/ب join (run-20261011-1252-rtl-step1-failfirst.txt). The two
	// subchecks stay independent so the SGR subcheck shows that bold is
	// observed even on the pre-fix output.
	pinRTLReorder(t)
	pinColorProfile(t)
	raw := renderAssistantBody(t, "**كت**ب", 80)
	t.Run("visual text", func(t *testing.T) {
		got := ansi.Strip(raw)
		want := formBehFinal + formTehMedial + formKafInitial
		if got != want {
			t.Fatalf("visual order:\n got %q (%s)\nwant %q (%s)",
				got, cps(got), want, cps(want))
		}
	})
	t.Run("bold runs", func(t *testing.T) {
		assertRuns(t, sgrRuns(raw), []sgrRun{
			{text: formBehFinal, bold: false},
			{text: formTehMedial + formKafInitial, bold: true},
		})
	})
}

func TestL2_CodeSpanNotShaped(t *testing.T) {
	// Guards P1: the pre-Step-1 pipeline shaped bytes inside backticks, so
	// the code span carried presentation forms (fail-first baseline in
	// run-20261011-1252-rtl-step1-failfirst.txt).
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "`مرحبا`", 80)
	got := ansi.Strip(raw)
	if got != "`مرحبا`" {
		t.Fatalf("code span bytes were reshaped (P1):\n got %q (%s)\nwant %q (%s)",
			got, cps(got), "`مرحبا`", cps("`مرحبا`"))
	}
}

func TestL2_CodeSpanIsLTRIsland(t *testing.T) {
	// Guards the Step 1 reorder: code span bytes stay literal and keep their
	// order; the island is never broken by the wrapper.
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "`مرحبا`", 80)
	if got := ansi.Strip(raw); got != "`مرحبا`" {
		t.Fatalf("code span corrupted:\n got %q (%s)\nwant %q", got, cps(got), "`مرحبا`")
	}
}

func TestL2_LamAlefLigature(t *testing.T) {
	// Guards the Step 1 reorder: pre-Step-1, `*` between ل and ا kept the
	// ligature from forming (got FE8D FEDD; fail-first baseline in
	// run-20261011-1252-rtl-step1-failfirst.txt).
	pinRTLReorder(t)
	pinColorProfile(t)
	raw := renderAssistantBody(t, "**ل**ا", 80)
	t.Run("visual text", func(t *testing.T) {
		got := ansi.Strip(raw)
		if got != formLamAlef {
			t.Fatalf("ligature:\n got %q (%s)\nwant %q (%s)",
				got, cps(got), formLamAlef, cps(formLamAlef))
		}
	})
	t.Run("bold runs", func(t *testing.T) {
		assertRuns(t, sgrRuns(raw), []sgrRun{{text: formLamAlef, bold: true}})
	})
}

func TestL2_CodeBreaksJoining(t *testing.T) {
	// Guards the Step 1 reorder: the letter inside backticks must stay
	// literal; pre-Step-1 it was shaped too (fail-first baseline in
	// run-20261011-1252-rtl-step1-failfirst.txt).
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "ب`ت`ت", 80)
	got := ansi.Strip(raw)
	want := formTehIsolated + "`ت`" + formBehIsolated
	if got != want {
		t.Fatalf("code bytes / joining:\n got %q (%s)\nwant %q (%s)",
			got, cps(got), want, cps(want))
	}
}

func TestL2_HeadingBoldBoundary(t *testing.T) {
	// Guards the Step 1 reorder for headings: pre-Step-1 the `# ` prefix was
	// stripped only after shaping, so the bold boundary broke the ت/ب join
	// (fail-first baseline in run-20261011-1252-rtl-step1-failfirst.txt).
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "# **كت**ب", 80)
	got := ansi.Strip(raw)
	want := formBehFinal + formTehMedial + formKafInitial
	if got != want {
		t.Fatalf("heading visual order:\n got %q (%s)\nwant %q (%s)",
			got, cps(got), want, cps(want))
	}
}

func TestL2_NarrowWidthSplitsWordAtSpanBoundary(t *testing.T) {
	// D2 golden: the wrapper CAN cut `كتب` at the boundary between the bold
	// span and the plain gap. Probe evidence for the pre-Step-1 pipeline
	// (same break structure, wrong forms):
	// docs/reports/raw/run-20261011-1317-rtl-step1-d2wrap-probe.txt block B.
	pinRTLReorder(t)
	t.Run("width 3 fits on one line", func(t *testing.T) {
		got := ansi.Strip(renderAssistantBody(t, "**كت**ب", 3))
		want := formBehFinal + formTehMedial + formKafInitial
		if got != want {
			t.Fatalf("got %q (%s), want %q (%s)", got, cps(got), want, cps(want))
		}
	})
	t.Run("width 2 splits at the span boundary", func(t *testing.T) {
		got := ansi.Strip(renderAssistantBody(t, "**كت**ب", 2))
		want := formTehMedial + formKafInitial + "\n" + formBehFinal
		if got != want {
			t.Fatalf("got %q (%s)\nwant %q (%s)", got, cps(got), want, cps(want))
		}
	})
	t.Run("width 1 splits inside the span too", func(t *testing.T) {
		got := ansi.Strip(renderAssistantBody(t, "**كت**ب", 1))
		want := formKafInitial + "\n" + formTehMedial + "\n" + formBehFinal
		if got != want {
			t.Fatalf("got %q (%s)\nwant %q (%s)", got, cps(got), want, cps(want))
		}
	})
}

// ── streaming ───────────────────────────────────────────────────────────

func TestL2_Protection_StreamingLiveTailIsNeverShaped(t *testing.T) {
	// Protection: the Chat/Replay live tail (partialTail) wraps — and may
	// reorder — the raw buffer but never shapes it, so raw letters survive
	// mid-stream and no presentation forms can appear before the turn ends.
	pinRTLReorder(t)
	tail := partialTail(sanitizeStreamText("**كتب"), 6, 80)
	if tail == "" {
		t.Fatal("live tail is empty")
	}
	if strings.ContainsAny(tail, shapedForms) {
		t.Fatalf("live tail was shaped: %q (%s)", tail, cps(tail))
	}
	for _, r := range "كتب" {
		if !strings.ContainsRune(tail, r) {
			t.Fatalf("live tail lost raw letter %q: %q (%s)", r, tail, cps(tail))
		}
	}
}

func TestL2_StreamingClosedTextIsStable(t *testing.T) {
	// The Feed re-renders the accumulated buffer on every batch. Two
	// properties: a prefix always renders identically (deterministic parse),
	// and closing the markers converges to the connected forms, identical to
	// a direct render of the same text.
	pinRTLReorder(t)
	chunks := []string{"**ك", "ت", "**", "ب"}
	buf := ""
	for _, c := range chunks {
		buf += c
		first := renderAssistantBody(t, buf, 80)
		second := renderAssistantBody(t, buf, 80)
		if first != second {
			t.Fatalf("prefix %q rendered differently on re-run:\n%q\n%q", buf, first, second)
		}
	}
	got := ansi.Strip(renderAssistantBody(t, "**كت**ب", 80))
	want := formBehFinal + formTehMedial + formKafInitial
	if got != want {
		t.Fatalf("closed stream did not converge:\n got %q (%s)\nwant %q (%s)",
			got, cps(got), want, cps(want))
	}
}

// ── L2 protections (pass on both pipelines by design) ───────────────────

func TestL2_Protection_ListWithArabic(t *testing.T) {
	// Protection, not fail-first: the list content is a single Arabic word
	// with no style boundary, so shaping before or after Markdown produces
	// the same connected forms. Renamed per review.
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "- كتاب", 80)
	got := ansi.Strip(raw)
	want := "- " + formBehIsolated + formAlefFinal + formTehMedial + formKafInitial
	if got != want {
		t.Fatalf("list line:\n got %q (%s)\nwant %q (%s)",
			got, cps(got), want, cps(want))
	}
}

func TestL2_Protection_UnclosedBold(t *testing.T) {
	// Protection: unclosed `**` is literal prose on both pipelines, so both
	// shape the same full-text run.
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "**كتاب", 80)
	got := ansi.Strip(raw)
	want := formBehIsolated + formAlefFinal + formTehMedial + formKafInitial + "**"
	if got != want {
		t.Fatalf("unclosed bold:\n got %q (%s)\nwant %q (%s)",
			got, cps(got), want, cps(want))
	}
}

func TestL2_Protection_Blockquote(t *testing.T) {
	// Protection: `> ` is not parsed, so the whole line is prose on both
	// pipelines.
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "> كتب", 80)
	got := ansi.Strip(raw)
	want := formBehFinal + formTehMedial + formKafInitial + " >"
	if got != want {
		t.Fatalf("blockquote:\n got %q (%s)\nwant %q (%s)",
			got, cps(got), want, cps(want))
	}
}

func TestL2_Protection_TableRow(t *testing.T) {
	// Protection: tables are not parsed, so the row is prose on both
	// pipelines.
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "| كتب |", 80)
	got := ansi.Strip(raw)
	want := "| " + formBehFinal + formTehMedial + formKafInitial + " |"
	if got != want {
		t.Fatalf("table row:\n got %q (%s)\nwant %q (%s)",
			got, cps(got), want, cps(want))
	}
}

func TestL2_Protection_FencedCodeBlock(t *testing.T) {
	// Protection: splitCodeBlocks already removes fenced blocks before
	// shaping, so their bytes stay literal on both pipelines.
	pinRTLReorder(t)
	raw := renderAssistantBody(t, "```\nكتب\n```", 80)
	got := ansi.Strip(raw)
	if !strings.Contains(got, "كتب") {
		t.Fatalf("fenced code content altered:\n got %q (%s)\nwant it to contain %q",
			got, cps(got), "كتب")
	}
	if strings.ContainsAny(got, shapedForms) {
		t.Fatalf("fenced code content was shaped:\n got %q (%s)", got, cps(got))
	}
}

func TestL2_Protection_WrappedArabic(t *testing.T) {
	// Protection: a boundary-free Arabic run wraps identically on both
	// pipelines; assert width safety and that no letters are lost or left
	// unshaped.
	pinRTLReorder(t)
	const width = 20
	raw := renderAssistantBody(t, strings.Repeat("كتب ", 12), width)
	got := ansi.Strip(raw)
	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("line %d width %d exceeds %d: %q", i, w, width, line)
		}
	}
	if n := strings.Count(got, formKafInitial); n != 12 {
		t.Fatalf("wrapping lost letters: found %d of 12 initial kafs\n%q", n, got)
	}
	if n := strings.Count(got, formBehFinal); n != 12 {
		t.Fatalf("wrapping lost letters: found %d of 12 final behs\n%q", n, got)
	}
	if strings.ContainsAny(got, "كتب") {
		t.Fatalf("wrapped prose still contains raw letters:\n got %q (%s)", got, cps(got))
	}
}

// ── characterizations (pre-existing rtl.Layout behaviour) ───────────────

func TestL2_Characterization_CodeSpanIsLTRIsland(t *testing.T) {
	// Characterization, not a Step 1 decision: rtl.Layout treats non-Prose
	// spans as LTR islands with an even embedding level and never reorders
	// their content (internal/rtl/layout.go:37-50, buildLineBaseline). The
	// adapter is called directly, so this does not depend on shaping.
	// Consequence recorded in the memo as an open question: on Termux (no
	// terminal BiDi) an Arabic word inside backticks renders in logical order
	// and unconnected — readable vs faithful is not decided here.
	whole := "`مرحبا`"
	lines, err := layoutRenderedText(whole,
		[]rtl.Span{{Start: 0, End: len(whole), Kind: rtl.Code, StyleID: styleNone}},
		80, rtl.Policy{Mode: rtl.Reorder, Base: rtl.Auto})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "\n"); got != whole {
		t.Fatalf("whole-line code span reordered:\n got %q (%s)\nwant %q",
			got, cps(got), whole)
	}

	mixed := "قال `مرحبا` جيدا"
	start := strings.Index(mixed, "`مرحبا`")
	if start < 0 {
		t.Fatalf("test input %q lost its code part", mixed)
	}
	lines, err = layoutRenderedText(mixed,
		[]rtl.Span{{Start: start, End: start + len("`مرحبا`"), Kind: rtl.Code, StyleID: styleNone}},
		80, rtl.Policy{Mode: rtl.Reorder, Base: rtl.Auto})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "`مرحبا`") {
		t.Fatalf("code span content was reordered:\n got %q (%s)\nwant it to contain %q",
			got, cps(got), "`مرحبا`")
	}
}

func TestL2_Characterization_BoldRunWrapsMidWord(t *testing.T) {
	// Characterization of PRE-EXISTING behaviour, independent of Step 1: a
	// styled run is ONE span, and every span is one atom for the wrapper, so
	// a bold run wider than the line is hard-filled at grapheme boundaries
	// and words break mid-word (probe block A:
	// docs/reports/raw/run-20261011-1317-rtl-step1-d2wrap-probe.txt).
	// Tracked as TECH_DEBT BOLD_RUN_WRAPS_MID_WORD; do not fold a fix into
	// the shaping move.
	word := formKafInitial + formTehMedial + formBehFinal
	run := word + " " + word + " " + word
	lines, err := layoutRenderedText(run,
		[]rtl.Span{{Start: 0, End: len(run), Kind: rtl.Prose, StyleID: styleBold}},
		6, rtl.Policy{Mode: rtl.Reorder, Base: rtl.Auto})
	if err != nil {
		t.Fatal(err)
	}
	got := ansi.Strip(strings.Join(lines, "\n"))
	want := formTehMedial + formKafInitial + " " + formBehFinal + formTehMedial + formKafInitial +
		"\n" + formBehFinal + formTehMedial + formKafInitial + " " + formBehFinal
	if got != want {
		t.Fatalf("bold-run wrap changed:\n got %q (%s)\nwant %q (%s)",
			got, cps(got), want, cps(want))
	}

	// Control: the same words without the atom break at spaces, and every
	// line holds whole words only (each line is reordered, so compare
	// against the visual word).
	reversedWord := formBehFinal + formTehMedial + formKafInitial
	plain, err := layoutRenderedText(run, nil, 6, rtl.Policy{Mode: rtl.Reorder, Base: rtl.Auto})
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range plain {
		stripped := strings.TrimSpace(ansi.Strip(l))
		if stripped != reversedWord {
			t.Fatalf("plain control line %d is not a whole word: %q (%s)",
				i, stripped, cps(stripped))
		}
	}
}

// ── invariants ──────────────────────────────────────────────────────────

// checkShapeProseRunsInvariants asserts the rebuilt-span contract for one
// input: spans are ordered, non-overlapping, bounded by the visual string,
// kinds are not reserved, and every Code span carries the literal logical
// code bytes in order.
func checkShapeProseRunsInvariants(t *testing.T, text string) {
	t.Helper()
	logical, spans := parseInlineSemantic(text, styleNone)
	visual, out := shapeProseRuns(logical, spans)

	var wantCode []string
	for _, s := range spans {
		if s.Kind != rtl.Prose {
			wantCode = append(wantCode, logical[s.Start:s.End])
		}
	}
	var gotCode []string
	prevEnd := 0
	for i, s := range out {
		if s.Kind != rtl.Prose && s.Kind != rtl.Code {
			t.Fatalf("input %q: span %d has reserved kind %d: %+v", text, i, s.Kind, s)
		}
		if s.Start < prevEnd {
			t.Fatalf("input %q: spans overlap or are unordered at %d: %+v", text, i, out)
		}
		if s.Start < 0 || s.Start >= s.End || s.End > len(visual) {
			t.Fatalf("input %q: span %d out of bounds (len visual %d): %+v", text, i, len(visual), s)
		}
		prevEnd = s.End
		if s.Kind != rtl.Prose {
			gotCode = append(gotCode, visual[s.Start:s.End])
		}
	}
	if len(gotCode) != len(wantCode) {
		t.Fatalf("input %q: %d islands, want %d: %+v", text, len(gotCode), len(wantCode), out)
	}
	for i := range gotCode {
		if gotCode[i] != wantCode[i] {
			t.Fatalf("input %q: island %d = %q, want literal %q", text, i, gotCode[i], wantCode[i])
		}
	}
}

func TestShapeProseRunsInvariants(t *testing.T) {
	// Deterministic generator: fixed seed so a failure reproduces exactly.
	rng := rand.New(rand.NewSource(20261011))
	alphabet := []rune{
		'ك', 'ت', 'ب', 'ل', 'ا', 'م', 'ر', 'ح', '\u064E', '\u0652',
		'*', '`', ' ', 'x', '1', '.', '-',
	}
	for i := 0; i < 500; i++ {
		n := rng.Intn(24)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		checkShapeProseRunsInvariants(t, sb.String())
	}
}

func TestShapeProseRunsRandomizedSweep(t *testing.T) {
	// Probabilistic sweep, NOT coverage-guided fuzzing: `-fuzz` is refused
	// on android/arm64 ("-fuzz flag is not supported"), so this is the local
	// substitute while FuzzShapeProseRuns stays available on linux. Time-
	// seeded; the seed is logged so any failure reproduces exactly. The
	// default count keeps the suite fast; evidence runs raise it with
	// NABD_RTL_SWEEP=<n>.
	iterations := 20000
	if v := os.Getenv("NABD_RTL_SWEEP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			iterations = n
		}
	}
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	alphabet := []rune{
		'ك', 'ت', 'ب', 'ل', 'ا', 'م', 'ر', 'ح', 'ع', 'س', 'ي', 'و',
		'\u0621', '\u0623', '\u0625', '\u0629', '\u0649',
		'\u064E', '\u0651', '\u0652', '\u0640', '\u200D', '\u200C',
		'\uFEDB', '\uFE98', '\uFE90', '\uFEFB',
		'*', '`', ' ', 'x', '1', '.', '-', ':', '/', '\n',
	}
	t.Logf("sweep seed=%d iterations=%d", seed, iterations)
	for i := 0; i < iterations; i++ {
		n := rng.Intn(32)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		checkShapeProseRunsInvariants(t, sb.String())
	}
}

func FuzzShapeProseRuns(f *testing.F) {
	for _, seed := range []string{
		"",
		"كتب",
		"**كت**ب",
		"`مرحبا`",
		"لا`x`",
		"**ل**ا",
		"ب`ت`ت",
		"**كَ**تَبَ",
		"**كتاب",
		"كتب https://example.com",
		"## **ل**ا `x`",
		"hello café",
		"123",
		"\U0001F468\u200D\U0001F469\u200D\U0001F467",
		"\uFEDB\uFE98\uFE90",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		checkShapeProseRunsInvariants(t, text)
	})
}
