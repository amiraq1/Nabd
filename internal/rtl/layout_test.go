package rtl

import (
	"strings"
	"testing"
)

func render(t *testing.T, text string, width int, p Policy) []VisualLine {
	t.Helper()
	return renderSpans(t, text, nil, width, p)
}

func renderSpans(t *testing.T, text string, spans []Span, width int, p Policy) []VisualLine {
	t.Helper()
	lines, err := Layout(text, spans, width, p)
	if err != nil {
		t.Fatalf("Layout(%q, %d spans): %v", text, len(spans), err)
	}
	return lines
}

func visualOf(lines []VisualLine) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, r := range l.Runs {
			for _, c := range r.Clusters {
				b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}

func clustersOf(lines []VisualLine) []Cluster {
	var out []Cluster
	for _, l := range lines {
		for _, r := range l.Runs {
			out = append(out, r.Clusters...)
		}
	}
	return out
}

// levelsByRune fills the logical level of every rune from one full-width line.
func levelsByRune(t *testing.T, input string, lines []VisualLine) []uint8 {
	t.Helper()
	lv := make([]uint8, len([]rune(input)))
	if len(lines) != 1 {
		t.Fatalf("expected a single line, got %d", len(lines))
	}
	for _, r := range lines[0].Runs {
		for _, c := range r.Clusters {
			for i := c.SrcRunes[0]; i < c.SrcRunes[1]; i++ {
				lv[i] = r.Level
			}
		}
	}
	return lv
}

// mustRestore reconstructs the logical text of one line via source mapping.
func mustRestore(t *testing.T, in string, lines []VisualLine) string {
	t.Helper()
	got, err := RestoreFromSource(in, clustersOf(lines))
	if err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}
	return got
}

// mustRestoreJoin reconstructs the whole input, joining lines with "\n".
func mustRestoreJoin(t *testing.T, in string, lines []VisualLine) string {
	t.Helper()
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		parts = append(parts, mustRestore(t, in, []VisualLine{l}))
	}
	return strings.Join(parts, "\n")
}

// TestVerifiedTCases checks all T1-T11 cases against the Spike 3.1 transcript
// values (cross-checked against FriBidi and the Unicode-verified engine). The
// cases carry no semantic spans, so their levels are exactly the pure-UBA
// values from the archive; atomic LTR islands are exercised separately in
// span_test.go with caller-supplied spans.
func TestVerifiedTCases(t *testing.T) {
	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			lines := render(t, tc.text, 120, Policy{Mode: ReorderAndMirror, Base: tc.dir})
			gotLevels := levelsByRune(t, tc.text, lines)
			if len(gotLevels) != len(tc.levels) {
				t.Fatalf("levels length %d, want %d", len(gotLevels), len(tc.levels))
			}
			for i := range tc.levels {
				if gotLevels[i] != tc.levels[i] {
					t.Fatalf("levels = %v, want %v", gotLevels, tc.levels)
				}
			}
			if tc.visual != "" {
				if got := visualOf(lines); got != tc.visual {
					t.Fatalf("visual = %q, want %q", got, tc.visual)
				}
			}
			if got := mustRestore(t, tc.text, lines); got != tc.text {
				t.Fatalf("logical restore = %q, want %q", got, tc.text)
			}
		})
	}
}

// TestResidualCanonicalCases renders NC1-NC4 with L4 mirroring applied.
func TestResidualCanonicalCases(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		dir    Direction
		visual string
	}{
		{"NC1", "a \u2329b.1\u3009", RTL, "a \u2329b.1\u3009"},
		{"NC2", "a \u3008b.1\u232A", RTL, "a \u3008b.1\u232A"},
		{"NC3", "\u05D0 \u2329\u05D1.1\u3009", LTR, "\u30081.\u05D1\u232A \u05D0"},
		{"NC4", "\u05D0 \u3008\u05D1.1\u232A", LTR, "\u23291.\u05D1\u3009 \u05D0"},
	}
	for _, tc := range cases {
		lines := render(t, tc.text, 120, Policy{Mode: ReorderAndMirror, Base: tc.dir})
		if got := visualOf(lines); got != tc.visual {
			t.Errorf("%s: visual = %q, want %q", tc.name, got, tc.visual)
		}
		if got := mustRestore(t, tc.text, lines); got != tc.text {
			t.Errorf("%s: restore = %q, want %q", tc.name, got, tc.text)
		}
	}
}

func TestBracketCases(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		dir    Direction
		levels []uint8
		visual string
	}{
		{
			name:   "canonical pair rtl",
			text:   "\u05D0\u05D1 \u2329\u05D2\u05D3\u3009 \u05D4\u05D5",
			dir:    Auto,
			levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			visual: "\u05D5\u05D4 \u3008\u05D3\u05D2\u232A \u05D1\u05D0",
		},
		{
			name:   "ascii angle rtl",
			text:   "\u05D0\u05D1 <\u05D2> \u05D3\u05D4",
			dir:    Auto,
			levels: []uint8{1, 1, 1, 1, 1, 1, 1, 1, 1},
			visual: "\u05D4\u05D3 <\u05D2> \u05D1\u05D0",
		},
		{
			name:   "ascii angle ltr",
			text:   "a <b> c",
			dir:    LTR,
			levels: []uint8{0, 0, 0, 0, 0, 0, 0},
			visual: "a <b> c",
		},
		{
			name:   "paren rtl",
			text:   "\u05D0 (b) \u05D1",
			dir:    RTL,
			levels: []uint8{1, 1, 1, 2, 1, 1, 1},
			visual: "\u05D1 (b) \u05D0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := render(t, tc.text, 120, Policy{Mode: ReorderAndMirror, Base: tc.dir})
			if got := levelsByRune(t, tc.text, lines); !equalLevels(got, tc.levels) {
				t.Fatalf("levels = %v, want %v", got, tc.levels)
			}
			if got := visualOf(lines); got != tc.visual {
				t.Fatalf("visual = %q, want %q", got, tc.visual)
			}
		})
	}
}

func equalLevels(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBookBrackets covers the standalone "ابج book(s)" differential case,
// verified against FriBidi 1.0.16 and the Unicode corpus engine.
func TestBookBrackets(t *testing.T) {
	text := "\u0627\u0628\u062C book(s)"
	cases := []struct {
		dir    Direction
		levels []uint8
		visual string
	}{
		{Auto, []uint8{1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2}, "book(s) \u062C\u0628\u0627"},
		{RTL, []uint8{1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2}, "book(s) \u062C\u0628\u0627"},
		{LTR, []uint8{1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0}, "\u062C\u0628\u0627 book(s)"},
	}
	for _, tc := range cases {
		lines := render(t, text, 120, Policy{Mode: ReorderAndMirror, Base: tc.dir})
		if got := levelsByRune(t, text, lines); !equalLevels(got, tc.levels) {
			t.Errorf("dir %d: levels = %v, want %v", tc.dir, got, tc.levels)
		}
		if got := visualOf(lines); got != tc.visual {
			t.Errorf("dir %d: visual = %q, want %q", tc.dir, got, tc.visual)
		}
	}
}

// TestClusterIntegrity: marks, lam-alef, emoji and ZWJ sequences survive
// reordering as whole grapheme clusters.
func TestClusterIntegrity(t *testing.T) {
	inputs := []string{
		"\u0645\u064E\u0631\u0652\u062D\u064E\u0628\u064B\u0627", // مَرْحَبًا
		"\u0644\u064E\u0627", // لَا
		"\u05D0\u05D1 \U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466 \u05D2\u05D3",
		"\u05D0\u05D1 \u2764\uFE0F \u05D2\u05D3",
		"\u05D0\u05D1 \U0001F44D\U0001F3FD \u05D2\u05D3",
	}
	for _, in := range inputs {
		lines := render(t, in, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
		if got := mustRestore(t, in, lines); got != in {
			t.Errorf("restore %q = %q", in, got)
		}
		// no cluster may start with a combining mark
		for _, c := range clustersOf(lines) {
			r := []rune(c.Text)[0]
			if isMarkRune(r) {
				t.Errorf("cluster %q starts with a combining mark", c.Text)
			}
		}
	}
	// The ZWJ family stays exactly one cluster of width 2.
	in := "\u05D0\u05D1 \U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466 \u05D2\u05D3"
	lines := render(t, in, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	found := false
	for _, c := range clustersOf(lines) {
		if strings.Contains(c.Text, "\U0001F468") {
			found = true
			if c.Text != "\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466" || c.Width != 2 {
				t.Errorf("family cluster = %q width %d", c.Text, c.Width)
			}
		}
	}
	if !found {
		t.Error("family emoji cluster not found")
	}
	// lam-alef keeps its mark attached: visual order is the two clusters
	// reversed, so "الَ" (alef then lam+fatha), never a bare mark.
	la := render(t, "\u0644\u064E\u0627", 120, Policy{Mode: ReorderAndMirror, Base: RTL})
	if got := visualOf(la); got != "\u0627\u0644\u064E" {
		t.Errorf("lam-alef visual = %q", got)
	}
}

func isMarkRune(r rune) bool {
	// Nonspacing/enclosing marks relevant to the clusters above.
	switch {
	case r >= 0x0300 && r <= 0x036F:
		return true
	case r >= 0x064B && r <= 0x065F:
		return true
	case r == 0x0670:
		return true
	case r >= 0x06D6 && r <= 0x06DC:
		return true
	case r >= 0xFE00 && r <= 0xFE0F:
		return true
	}
	return false
}

// TestAtomicSpans: caller-supplied Code, Path and URL spans stay intact and
// render as LTR islands, including brackets inside them. internal/rtl does no
// Markdown, URL or path detection; the spans below stand in for the semantic
// Markdown layer that lands in PR 2.
func TestAtomicSpans(t *testing.T) {
	spanOf := func(text, sub string, kind SpanKind, style uint16) Span {
		t.Helper()
		start := strings.Index(text, sub)
		if start < 0 {
			t.Fatalf("%q not found in %q", sub, text)
		}
		return Span{Start: start, End: start + len(sub), Kind: kind, StyleID: style}
	}
	cases := []struct {
		text string
		sub  string
		kind SpanKind
	}{
		{"xx a/b yy", "a/b", Path},
		{"ab `go test` cd", "`go test`", Code},
		{"\u05D0\u05D1 visit https://example.com/a(b) \u05D2\u05D3", "https://example.com/a(b)", URL},
		{"\u05D0\u05D1 `(x)` \u05D2\u05D3", "`(x)`", Code},
		{"\u05D0\u05D1 internal/ui/v2.1.4.go \u05D2\u05D3", "internal/ui/v2.1.4.go", Path},
	}

	// A short path span never splits at width 4.
	lines := renderSpans(t, cases[0].text, []Span{spanOf(cases[0].text, cases[0].sub, cases[0].kind, 1)}, 4,
		Policy{Mode: ReorderAndMirror, Base: Auto})
	for _, l := range lines {
		got := visualOf([]VisualLine{l})
		if strings.Contains(got, "a/") && !strings.Contains(got, "a/b") {
			t.Fatalf("path split across lines: %q", got)
		}
	}

	// A code span with spaces is atomic when it fits.
	lines = renderSpans(t, cases[1].text, []Span{spanOf(cases[1].text, cases[1].sub, cases[1].kind, 2)}, 10,
		Policy{Mode: ReorderAndMirror, Base: Auto})
	inOneLine := false
	for _, l := range lines {
		if strings.Contains(visualOf([]VisualLine{l}), "`go test`") {
			inOneLine = true
		}
	}
	if !inOneLine {
		t.Errorf("code span split: %q", visualOf(lines))
	}

	// Brackets inside URL and code spans are not mirrored even in RTL text,
	// and a path with a digit stays an LTR island.
	for _, i := range []int{2, 3, 4} {
		ls := renderSpans(t, cases[i].text, []Span{spanOf(cases[i].text, cases[i].sub, cases[i].kind, uint16(i))}, 120,
			Policy{Mode: ReorderAndMirror, Base: RTL})
		if got := visualOf(ls); !strings.Contains(got, cases[i].sub) {
			t.Errorf("span %q mangled: %q", cases[i].sub, got)
		}
	}

	// Source mapping survives every case.
	for i, c := range cases {
		ls := renderSpans(t, c.text, []Span{spanOf(c.text, c.sub, c.kind, uint16(i))}, 120,
			Policy{Mode: ReorderAndMirror, Base: Auto})
		if got := mustRestore(t, c.text, ls); got != c.text {
			t.Errorf("restore %q = %q", c.text, got)
		}
	}
}

func TestModes(t *testing.T) {
	text := "(\u0645\u0631\u062D\u0628\u0627) [go]"
	logical := render(t, text, 120, Policy{Mode: Logical, Base: Auto})
	if got := visualOf(logical); got != text {
		t.Errorf("Logical visual = %q, want input", got)
	}
	reorder := render(t, text, 120, Policy{Mode: Reorder, Base: Auto})
	if got := visualOf(reorder); got != "]go[ )\u0627\u0628\u062D\u0631\u0645(" {
		t.Errorf("Reorder visual = %q", got)
	}
	mirror := render(t, text, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	if got := visualOf(mirror); got != "[go] (\u0627\u0628\u062D\u0631\u0645)" {
		t.Errorf("ReorderAndMirror visual = %q", got)
	}
}

func TestLineSplitting(t *testing.T) {
	in := "\u05D0\u05D1\nabc\n\n\u05D2\u05D3"
	lines := render(t, in, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4", len(lines))
	}
	if got := mustRestoreJoin(t, in, lines); got != in {
		t.Errorf("restore with newlines = %q, want %q", got, in)
	}
}

func TestLayoutErrors(t *testing.T) {
	if _, err := Layout("x", nil, 0, Policy{}); err != ErrInvalidWidth {
		t.Errorf("width error = %v", err)
	}
	if _, err := Layout("x", nil, 10, Policy{Mode: 9}); err != ErrInvalidPolicy {
		t.Errorf("mode error = %v", err)
	}
	if _, err := Layout("x", nil, 10, Policy{Base: 9}); err != ErrInvalidPolicy {
		t.Errorf("base error = %v", err)
	}
	lines := render(t, "", 10, Policy{})
	if len(lines) != 1 || lines[0].Width != 0 || len(lines[0].Runs) != 0 {
		t.Errorf("empty input lines = %+v", lines)
	}
}
