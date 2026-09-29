package rtl

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// spanOf builds a span from the first occurrence of sub in text.
func spanOf(t *testing.T, text, sub string, kind SpanKind, style uint16) Span {
	t.Helper()
	start := strings.Index(text, sub)
	if start < 0 {
		t.Fatalf("%q not found in %q", sub, text)
	}
	return Span{Start: start, End: start + len(sub), Kind: kind, StyleID: style}
}

func runKindsStyles(lines []VisualLine) ([]SpanKind, []uint16) {
	var kinds []SpanKind
	var styles []uint16
	for _, l := range lines {
		for _, r := range l.Runs {
			kinds = append(kinds, r.Kind)
			styles = append(styles, r.StyleID)
		}
	}
	return kinds, styles
}

// --- valid inputs ---

func TestNilAndEmptySpansAreAllProse(t *testing.T) {
	text := "\u0645\u0631\u062d\u0628\u0627 hello"
	nilLines := renderSpans(t, text, nil, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	emptyLines := renderSpans(t, text, []Span{}, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	if visualOf(nilLines) != visualOf(emptyLines) {
		t.Fatalf("nil and empty spans differ: %q vs %q", visualOf(nilLines), visualOf(emptyLines))
	}
	kinds, styles := runKindsStyles(nilLines)
	for i := range kinds {
		if kinds[i] != Prose || styles[i] != 0 {
			t.Fatalf("nil spans must be all prose style 0, got kind=%d style=%d", kinds[i], styles[i])
		}
	}
}

func TestSingleSpan(t *testing.T) {
	text := "abc"
	lines := renderSpans(t, text, []Span{{Start: 0, End: 3, Kind: Code, StyleID: 9}}, 120,
		Policy{Mode: ReorderAndMirror, Base: LTR})
	kinds, styles := runKindsStyles(lines)
	if len(kinds) != 1 || kinds[0] != Code || styles[0] != 9 {
		t.Fatalf("single span runs = %v / %v", kinds, styles)
	}
	if got := mustRestore(t, text, lines); got != text {
		t.Fatalf("restore = %q", got)
	}
}

func TestMultipleSpansWithProseGaps(t *testing.T) {
	text := "aa bb cc"
	spans := []Span{
		spanOf(t, text, "bb", Code, 1),
		spanOf(t, text, "cc", Path, 2),
	}
	lines := renderSpans(t, text, spans, 120, Policy{Mode: ReorderAndMirror, Base: LTR})
	kinds, styles := runKindsStyles(lines)
	wantKinds := []SpanKind{Prose, Code, Prose, Path}
	wantStyles := []uint16{0, 1, 0, 2}
	if len(kinds) != len(wantKinds) {
		t.Fatalf("runs = %v / %v", kinds, styles)
	}
	for i := range wantKinds {
		if kinds[i] != wantKinds[i] || styles[i] != wantStyles[i] {
			t.Fatalf("runs = %v / %v, want %v / %v", kinds, styles, wantKinds, wantStyles)
		}
	}
}

func TestAdjacentSameKindDifferentStyle(t *testing.T) {
	text := "abcdef"
	spans := []Span{
		{Start: 0, End: 3, Kind: Code, StyleID: 1},
		{Start: 3, End: 6, Kind: Code, StyleID: 2},
	}
	lines := renderSpans(t, text, spans, 120, Policy{Mode: ReorderAndMirror, Base: LTR})
	kinds, styles := runKindsStyles(lines)
	if len(kinds) != 2 || kinds[0] != Code || kinds[1] != Code {
		t.Fatalf("runs = %v / %v", kinds, styles)
	}
	if styles[0] != 1 || styles[1] != 2 {
		t.Fatalf("adjacent runs merged across StyleID: %v", styles)
	}
}

func TestAdjacentDifferentKinds(t *testing.T) {
	text := "abcdef"
	spans := []Span{
		{Start: 0, End: 3, Kind: Code, StyleID: 1},
		{Start: 3, End: 6, Kind: Path, StyleID: 1},
	}
	lines := renderSpans(t, text, spans, 120, Policy{Mode: ReorderAndMirror, Base: LTR})
	kinds, _ := runKindsStyles(lines)
	if len(kinds) != 2 || kinds[0] != Code || kinds[1] != Path {
		t.Fatalf("runs = %v", kinds)
	}
}

func TestArabicProseWithPath(t *testing.T) {
	text := "\u0627\u0641\u062A\u062D internal/ui/feed.go \u062B\u0645"
	span := spanOf(t, text, "internal/ui/feed.go", Path, 3)
	lines := renderSpans(t, text, []Span{span}, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	if got := visualOf(lines); !strings.Contains(got, "internal/ui/feed.go") {
		t.Fatalf("path mangled: %q", got)
	}
	assertSpanLevelsEven(t, text, span, lines)
	if got := mustRestore(t, text, lines); got != text {
		t.Fatalf("restore = %q", got)
	}
}

func TestArabicProseWithCodePathCommand(t *testing.T) {
	text := "\u0645\u0631\u062d\u0628\u0627 `code` plain sub/dir run-now"
	spans := []Span{
		spanOf(t, text, "`code`", Code, 1),
		spanOf(t, text, "sub/dir", Path, 2),
		spanOf(t, text, "run-now", Command, 3),
	}
	lines := renderSpans(t, text, spans, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	kinds, styles := runKindsStyles(lines)
	for _, want := range []struct {
		kind SpanKind
		st   uint16
	}{{Code, 1}, {Path, 2}, {Command, 3}} {
		found := false
		for i := range kinds {
			if kinds[i] == want.kind && styles[i] == want.st {
				found = true
			}
		}
		if !found {
			t.Fatalf("run for kind %d style %d missing: %v / %v", want.kind, want.st, kinds, styles)
		}
	}
	for _, s := range spans {
		assertSpanLevelsEven(t, text, s, lines)
	}
}

// assertSpanLevelsEven proves the LTR-island contract: every cluster whose
// source range lies inside span has an even run level.
func assertSpanLevelsEven(t *testing.T, text string, span Span, lines []VisualLine) {
	t.Helper()
	for _, l := range lines {
		for _, r := range l.Runs {
			for _, c := range r.Clusters {
				if c.SrcBytes[0] >= span.Start && c.SrcBytes[1] <= span.End {
					if r.Level%2 != 0 {
						t.Fatalf("span [%d,%d) cluster %q has odd level %d", span.Start, span.End, c.Text, r.Level)
					}
				}
			}
		}
	}
}

func TestUTF8ArabicByteRanges(t *testing.T) {
	text := "\u0645\u0631\u062d\u0628\u0627 world"
	// م ر ح ب ا are 2 bytes each; the space adds one: the span must start at 11.
	span := spanOf(t, text, "world", Code, 1)
	if span.Start != 11 {
		t.Fatalf("span start = %d, want 11 (byte offsets)", span.Start)
	}
	lines := renderSpans(t, text, []Span{span}, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	if got := visualOf(lines); !strings.Contains(got, "world") {
		t.Fatalf("visual = %q", got)
	}
}

func TestEmojiAndZWJClusterBoundaries(t *testing.T) {
	family := "\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466"
	text := "\u05D0\u05D1 " + family + " cd"
	span := spanOf(t, text, family, Code, 5)
	lines := renderSpans(t, text, []Span{span}, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	if got := visualOf(lines); !strings.Contains(got, family) {
		t.Fatalf("family split or reordered: %q", got)
	}
	if got := mustRestore(t, text, lines); got != text {
		t.Fatalf("restore = %q", got)
	}
}

func TestCombiningMarkSpanBoundaries(t *testing.T) {
	text := "e\u0301x"
	span := Span{Start: 0, End: len("e\u0301"), Kind: Code, StyleID: 1}
	lines := renderSpans(t, text, []Span{span}, 120, Policy{Mode: ReorderAndMirror, Base: LTR})
	kinds, _ := runKindsStyles(lines)
	if len(kinds) != 2 || kinds[0] != Code || kinds[1] != Prose {
		t.Fatalf("runs = %v", kinds)
	}
}

func TestSpanCoversWholeLine(t *testing.T) {
	text := "\u05D0\u05D1 (cd) \u05D2\u05D3"
	span := Span{Start: 0, End: len(text), Kind: Code, StyleID: 4}
	lines := renderSpans(t, text, []Span{span}, 120, Policy{Mode: ReorderAndMirror, Base: RTL})
	if got := visualOf(lines); got != text {
		t.Fatalf("whole-line span must stay LTR and unmirrored: %q", got)
	}
	kinds, styles := runKindsStyles(lines)
	for i := range kinds {
		if kinds[i] != Code || styles[i] != 4 {
			t.Fatalf("run %d kind/style = %d/%d", i, kinds[i], styles[i])
		}
	}
}

func TestSpansAcrossMultipleVisualLines(t *testing.T) {
	text := "\u0645\u0631\u062d\u0628\u0627 \u0628\u0627\u0644\u0639\u0627\u0644\u0645 alpha beta gamma delta"
	spans := []Span{
		spanOf(t, text, "alpha beta", Code, 1),
		spanOf(t, text, "gamma", Path, 2),
	}
	lines := renderSpans(t, text, spans, 20, Policy{Mode: ReorderAndMirror, Base: Auto})
	if len(lines) < 2 {
		t.Fatalf("expected multiple visual lines, got %d", len(lines))
	}
	for _, s := range spans {
		assertSpanLevelsEven(t, text, s, lines)
	}
	if got := mustRestore(t, text, lines); got != text {
		t.Fatalf("restore = %q", got)
	}
}

func TestStyleIDSurvivesReorderAndMirror(t *testing.T) {
	// A Prose span keeps its StyleID while L2/L4 act on it.
	text := "\u05D0 (\u05D1) \u05D2"
	span := Span{Start: 0, End: len(text), Kind: Prose, StyleID: 3}
	lines := renderSpans(t, text, []Span{span}, 120, Policy{Mode: ReorderAndMirror, Base: RTL})
	foundMirrored := false
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.StyleID != 3 {
				t.Fatalf("run style = %d, want 3", r.StyleID)
			}
			for _, c := range r.Clusters {
				src, _ := SourceText(text, c)
				if src == "(" && c.Text == ")" || src == ")" && c.Text == "(" {
					foundMirrored = true
					if r.Kind != Prose {
						t.Fatalf("mirrored cluster kind = %d", r.Kind)
					}
				}
			}
		}
	}
	if !foundMirrored {
		t.Fatalf("no mirrored bracket found in %q", visualOf(lines))
	}
}

func TestStyleIDNeverEntersCopiedText(t *testing.T) {
	text := "\u0645\u0631\u062d\u0628\u0627"
	lines := renderSpans(t, text, []Span{{Start: 0, End: len(text), Kind: Code, StyleID: 4242}}, 120,
		Policy{Mode: ReorderAndMirror, Base: Auto})
	if got := mustRestore(t, text, lines); got != text {
		t.Fatalf("copy = %q, want %q", got, text)
	}
	for _, l := range lines {
		for _, r := range l.Runs {
			for _, c := range r.Clusters {
				if strings.Contains(c.Text, "4242") {
					t.Fatalf("style id leaked into cluster text %q", c.Text)
				}
			}
		}
	}
}

// --- invalid inputs ---

func TestInvalidSpans(t *testing.T) {
	text := "abcdef"
	euro := "\u20AC" // 3 bytes
	combining := "e\u0301"
	family := "\U0001F468\u200D\U0001F469"
	zwj := strings.Index(family, "\u200D")

	cases := []struct {
		name  string
		text  string
		spans []Span
	}{
		{"negative start", text, []Span{{Start: -1, End: 3, Kind: Code}}},
		{"negative end", text, []Span{{Start: 2, End: -3, Kind: Code}}},
		{"end beyond input", text, []Span{{Start: 0, End: 7, Kind: Code}}},
		{"start equals end", text, []Span{{Start: 2, End: 2, Kind: Code}}},
		{"start after end", text, []Span{{Start: 4, End: 2, Kind: Code}}},
		{"unsorted", text, []Span{{Start: 4, End: 5}, {Start: 1, End: 2}}},
		{"overlapping", text, []Span{{Start: 0, End: 4}, {Start: 2, End: 6}}},
		{"inside utf8 rune", euro, []Span{{Start: 0, End: 1, Kind: Code}}},
		{"end inside utf8 rune", euro, []Span{{Start: 0, End: 2, Kind: Code}}},
		{"inside grapheme cluster (mark)", combining, []Span{{Start: 0, End: 1, Kind: Code}}},
		{"inside grapheme cluster (zwj boundary)", family, []Span{{Start: 0, End: zwj + 3, Kind: Code}}},
		{"inside rune of zwj sequence", family, []Span{{Start: 0, End: zwj + 1, Kind: Code}}},
		{"beyond empty input", "", []Span{{Start: 0, End: 1, Kind: Code}}},
		{"extreme range", text, []Span{{Start: math.MinInt, End: math.MaxInt, Kind: Code}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := Layout(tc.text, tc.spans, 40, Policy{Mode: ReorderAndMirror, Base: Auto})
			if err == nil {
				t.Fatalf("expected ErrInvalidSpans, got lines=%v", lines)
			}
			if !errors.Is(err, ErrInvalidSpans) {
				t.Fatalf("error = %v, want ErrInvalidSpans", err)
			}
			if lines != nil {
				t.Fatalf("lines must be nil on error, got %v", lines)
			}
		})
	}
}

func TestValidBoundaryCases(t *testing.T) {
	text := "e\u0301" + "\u20AC" // e + combining acute, then euro
	spans := []Span{
		{Start: 0, End: 3, Kind: Code}, // the whole e+mark cluster
		{Start: 3, End: 6, Kind: Path}, // the whole euro rune
	}
	if _, err := Layout(text, spans, 40, Policy{Mode: ReorderAndMirror, Base: Auto}); err != nil {
		t.Fatalf("valid boundaries rejected: %v", err)
	}
	// A span is allowed to cover whitespace when the caller emits it.
	ws := "aa  bb"
	if _, err := Layout(ws, []Span{{Start: 2, End: 3, Kind: Code}}, 40, Policy{Mode: ReorderAndMirror, Base: Auto}); err != nil {
		t.Fatalf("whitespace span rejected: %v", err)
	}
}

// --- atomicity ---

func TestAdjacentCodeAndPathSpansAreSeparateAtoms(t *testing.T) {
	text := "ab/cd" // Code "ab" immediately followed by Path "/cd"
	spans := []Span{
		{Start: 0, End: 2, Kind: Code, StyleID: 1},
		{Start: 2, End: 5, Kind: Path, StyleID: 1},
	}
	lines := renderSpans(t, text, spans, 3, Policy{Mode: ReorderAndMirror, Base: LTR})
	var got []string
	for _, l := range lines {
		got = append(got, visualOf([]VisualLine{l}))
	}
	want := []string{"ab", "/cd"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("lines = %v, want %v (spans must not merge)", got, want)
	}
}

func TestAdjacentCodeSpansAreSeparateAtoms(t *testing.T) {
	text := "abcdef"
	spans := []Span{
		{Start: 0, End: 3, Kind: Code, StyleID: 1},
		{Start: 3, End: 6, Kind: Code, StyleID: 1},
	}
	lines := renderSpans(t, text, spans, 4, Policy{Mode: ReorderAndMirror, Base: LTR})
	var got []string
	for _, l := range lines {
		got = append(got, visualOf([]VisualLine{l}))
	}
	want := []string{"abc", "def"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("lines = %v, want %v", got, want)
	}
}

func TestLongSpanOverflowPolicy(t *testing.T) {
	text := "abcdefgh"
	span := Span{Start: 0, End: len(text), Kind: Code, StyleID: 1}
	lines := renderSpans(t, text, []Span{span}, 3, Policy{Mode: ReorderAndMirror, Base: LTR})
	var got []string
	for _, l := range lines {
		got = append(got, visualOf([]VisualLine{l}))
	}
	want := []string{"abc", "def", "gh"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("overflow split = %v, want %v (documented policy)", got, want)
	}
	if r := mustRestore(t, text, lines); r != text {
		t.Fatalf("overflow lost text: %q", r)
	}
	// Every written line is at most the limit because the span is split only
	// at grapheme-cluster boundaries once it alone exceeds the width.
	for _, l := range lines {
		if l.Width > 3 {
			t.Fatalf("line width %d exceeds limit", l.Width)
		}
	}
}

func TestProseDoesNotInheritAtomicity(t *testing.T) {
	text := "xx yy zz"
	spans := []Span{spanOf(t, text, "yy", Code, 1)}
	lines := renderSpans(t, text, spans, 3, Policy{Mode: ReorderAndMirror, Base: LTR})
	var got []string
	for _, l := range lines {
		got = append(got, visualOf([]VisualLine{l}))
	}
	want := []string{"xx ", "yy ", "zz"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
	// Gap clusters stay prose with StyleID 0 even next to a Code span.
	for _, l := range lines {
		for _, r := range l.Runs {
			for _, c := range r.Clusters {
				if c.SrcBytes[0] >= 3 && c.SrcBytes[1] <= 5 {
					continue // inside the "yy" span
				}
				if r.Kind != Prose || r.StyleID != 0 {
					t.Fatalf("gap cluster %q kind/style = %d/%d", c.Text, r.Kind, r.StyleID)
				}
			}
		}
	}
}
