package rtl

import (
	"bytes"
	"strings"
	"testing"

	"nabd/internal/rtl/bidi"
)

// renderShaped runs layout with internal arabicShaping option enabled.
func renderShaped(t *testing.T, text string, spans []Span, width int, p Policy) []VisualLine {
	t.Helper()
	lines, err := layoutWithOptions(text, spans, width, p, layoutOptions{arabicShaping: true})
	if err != nil {
		t.Fatalf("layoutWithOptions(arabicShaping: true): %v", err)
	}
	return lines
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. Baseline Identity Assertion (Default Gate OFF)
// ─────────────────────────────────────────────────────────────────────────────

// TestLayoutShapingDisabledMatchesBaseline asserts that the public Layout API
// produces identical output to the baseline across all test cases, benchmark
// fixtures, and semantic span configurations.
func TestLayoutShapingDisabledMatchesBaseline(t *testing.T) {
	fixtures := []struct {
		name   string
		text   string
		spans  []Span
		width  int
		policy Policy
	}{
		{
			name:   "benchMixed Auto",
			text:   benchMixed,
			width:  50,
			policy: Policy{Mode: ReorderAndMirror, Base: Auto},
		},
		{
			name:   "benchMixed Logical",
			text:   benchMixed,
			width:  50,
			policy: Policy{Mode: Logical, Base: Auto},
		},
		{
			name:   "benchMixed Reorder",
			text:   benchMixed,
			width:  50,
			policy: Policy{Mode: Reorder, Base: Auto},
		},
		{
			name:   "plain ASCII",
			text:   "internal/ui/feed.go go test ./... and more plain ascii text",
			width:  50,
			policy: Policy{Mode: ReorderAndMirror, Base: LTR},
		},
	}

	// Add all tcases under multiple modes.
	for _, tc := range tcases {
		for _, m := range []Mode{Logical, Reorder, ReorderAndMirror} {
			fixtures = append(fixtures, struct {
				name   string
				text   string
				spans  []Span
				width  int
				policy Policy
			}{
				name:   tc.name + " Mode=" + modeName(m),
				text:   tc.text,
				width:  120,
				policy: Policy{Mode: m, Base: tc.dir},
			})
		}
	}

	// Add widthInputs under multiple widths.
	for _, w := range []int{20, 40, 80} {
		for _, wi := range widthInputs {
			fixtures = append(fixtures, struct {
				name   string
				text   string
				spans  []Span
				width  int
				policy Policy
			}{
				name:   "widthInput w=" + string(rune('0'+w/10)),
				text:   wi,
				width:  w,
				policy: Policy{Mode: ReorderAndMirror, Base: Auto},
			})
		}
	}

	// Add semantic span fixtures: Prose, Code, Path, URL, Command.
	spanText := "مرحبا بالعالم go test ./... النتيجة"
	fixtures = append(fixtures, struct {
		name   string
		text   string
		spans  []Span
		width  int
		policy Policy
	}{
		name: "Semantic Spans Mixed",
		text: spanText,
		spans: []Span{
			{Start: 0, End: 25, Kind: Prose, StyleID: 1},
			{Start: 25, End: 38, Kind: Code, StyleID: 2},
			{Start: 38, End: len(spanText), Kind: Prose, StyleID: 3},
		},
		width:  60,
		policy: Policy{Mode: ReorderAndMirror, Base: Auto},
	})

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			got, err := Layout(f.text, f.spans, f.width, f.policy)
			if err != nil {
				t.Fatalf("Layout failed: %v", err)
			}

			want := baselineLayout(t, f.text, f.spans, f.width, f.policy)

			gotBytes := serializeVisualLines(got)
			wantBytes := serializeVisualLines(want)

			if !bytes.Equal(gotBytes, wantBytes) {
				t.Fatalf("disabled shaping changed baseline output for fixture %q\ngot:\n%s\nwant:\n%s",
					f.name, string(gotBytes), string(wantBytes))
			}
		})
	}
}

// baselineLayout computes layout using the baseline path directly.
func baselineLayout(t *testing.T, text string, spans []Span, width int, p Policy) []VisualLine {
	t.Helper()
	clusters := clusterize(text)
	if err := validateSpans(text, spans, clusters); err != nil {
		t.Fatalf("validateSpans: %v", err)
	}
	classified := classifyClusters(clusters, spans)

	var lines [][]classifiedCluster
	cur := make([]classifiedCluster, 0, len(classified))
	for _, c := range classified {
		if strings.ContainsRune(c.Text, '\n') {
			lines = append(lines, cur)
			cur = make([]classifiedCluster, 0, 8)
			continue
		}
		cur = append(cur, c)
	}
	lines = append(lines, cur)

	out := make([]VisualLine, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			out = append(out, VisualLine{})
			continue
		}

		var paraRunes []rune
		for _, cc := range line {
			paraRunes = append(paraRunes, []rune(cc.Text)...)
		}

		paraBase := -1
		switch p.Base {
		case LTR:
			paraBase = 0
		case RTL:
			paraBase = 1
		}
		asciiPara := p.Base != RTL && isSimpleASCII(string(paraRunes))

		pieces := splitPieces(line, width)

		runeStart := make([]int, len(line)+1)
		for i, cc := range line {
			runeStart[i+1] = runeStart[i] + len([]rune(cc.Text))
		}

		linebreaks := make([]int, len(pieces))
		for pi := range pieces {
			if pi < len(pieces)-1 {
				nextStart := pieces[pi+1][0]
				linebreaks[pi] = runeStart[nextStart]
			} else {
				linebreaks[pi] = len(paraRunes)
			}
		}

		var paraAnalysisParaLevel uint8
		var pieceLevels [][]uint8
		if !asciiPara {
			ana, pl, err := bidi.AnalyzeWithLineBreaks(paraRunes, paraBase, linebreaks)
			if err != nil {
				t.Fatalf("bidi: %v", err)
			}
			paraAnalysisParaLevel = ana.ParaLevel
			pieceLevels = pl
		}

		for pi, piece := range pieces {
			var pLevels []uint8
			if !asciiPara {
				pLevels = pieceLevels[pi]
			}
			vl, err := buildLineBaseline(line, piece, p, paraAnalysisParaLevel, pLevels, runeStart, asciiPara)
			if err != nil {
				t.Fatalf("buildLineBaseline: %v", err)
			}
			out = append(out, vl)
		}
	}
	return out
}

func serializeVisualLines(lines []VisualLine) []byte {
	var b strings.Builder
	for li, l := range lines {
		b.WriteString(strings.Repeat("-", 10))
		b.WriteString("\n")
		b.WriteString("Line ")
		b.WriteString(string(rune('0' + li)))
		b.WriteString(" Width=")
		b.WriteString(string(rune('0' + l.Width)))
		b.WriteString("\n")
		for ri, r := range l.Runs {
			b.WriteString("  Run ")
			b.WriteString(string(rune('0' + ri)))
			b.WriteString(" Level=")
			b.WriteString(string(rune('0' + r.Level)))
			b.WriteString(" Kind=")
			b.WriteString(string(rune('0' + r.Kind)))
			b.WriteString(" StyleID=")
			b.WriteString(string(rune('0' + r.StyleID)))
			b.WriteString("\n")
			for ci, c := range r.Clusters {
				b.WriteString("    Cluster ")
				b.WriteString(string(rune('0' + ci)))
				b.WriteString(": Text=")
				b.WriteString(c.Text)
				b.WriteString(" W=")
				b.WriteString(string(rune('0' + c.Width)))
				b.WriteString(" Runes=[")
				b.WriteString(string(rune('0' + c.SrcRunes[0])))
				b.WriteString(",")
				b.WriteString(string(rune('0' + c.SrcRunes[1])))
				b.WriteString("] Bytes=[")
				b.WriteString(string(rune('0' + c.SrcBytes[0])))
				b.WriteString(",")
				b.WriteString(string(rune('0' + c.SrcBytes[1])))
				b.WriteString("]\n")
			}
		}
	}
	return []byte(b.String())
}

func modeName(m Mode) string {
	switch m {
	case Logical:
		return "Logical"
	case Reorder:
		return "Reorder"
	case ReorderAndMirror:
		return "ReorderAndMirror"
	default:
		return "Unknown"
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Boundary Tests: Proving Non-Crossing of Runs
// ─────────────────────────────────────────────────────────────────────────────

// TestBoundaryArabicCodeArabic proves that runs do NOT cross Code boundaries.
// Code spans are LTR islands and must NEVER enter ShapeArabic.
func TestBoundaryArabicCodeArabic(t *testing.T) {
	// "كتب " (Prose) + "ls" (Code) + " كتب" (Prose)
	// U+0643 U+062A U+0628 ' ' 'l' 's' ' ' U+0643 U+062A U+0628
	text := "\u0643\u062A\u0628 ls \u0643\u062A\u0628"
	spans := []Span{
		{Start: 0, End: 7, Kind: Prose, StyleID: 0},
		{Start: 7, End: 9, Kind: Code, StyleID: 0},
		{Start: 9, End: 16, Kind: Prose, StyleID: 0},
	}

	lines := renderShaped(t, text, spans, 80, Policy{Mode: Logical, Base: RTL})

	// Hand-authored expected runes:
	// Span 0: Kaf initial (0xFEDB), Teh medial (0xFE98), Beh final (0xFE90), Space (' ')
	wantSpan0 := []rune{0xFEDB, 0xFE98, 0xFE90, ' '}
	// Span 1: Code span NEVER shaped: 'l', 's'
	wantSpan1 := []rune{'l', 's'}
	// Span 2: Space (' '), Kaf initial (0xFEDB), Teh medial (0xFE98), Beh final (0xFE90)
	wantSpan2 := []rune{' ', 0xFEDB, 0xFE98, 0xFE90}

	var gotClusters []Cluster
	for _, r := range lines[0].Runs {
		gotClusters = append(gotClusters, r.Clusters...)
	}

	if len(gotClusters) != 10 {
		t.Fatalf("got %d clusters, want 10", len(gotClusters))
	}

	for i, wantR := range wantSpan0 {
		if got := []rune(gotClusters[i].Text); len(got) != 1 || got[0] != wantR {
			t.Fatalf("Span 0 cluster %d: got %U, want %U", i, got, wantR)
		}
	}
	for i, wantR := range wantSpan1 {
		c := gotClusters[4+i]
		if got := []rune(c.Text); len(got) != 1 || got[0] != wantR {
			t.Fatalf("Code cluster %d: got %U, want %U", i, got, wantR)
		}
	}
	for i, wantR := range wantSpan2 {
		if got := []rune(gotClusters[6+i].Text); len(got) != 1 || got[0] != wantR {
			t.Fatalf("Span 2 cluster %d: got %U, want %U", i, got, wantR)
		}
	}

	restored, err := RestoreFromSource(text, gotClusters)
	if err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}
	if restored != text {
		t.Fatalf("restored = %q, want %q", restored, text)
	}
}

// TestBoundaryArabicPathUnchanged proves that Path spans are LTR islands and NEVER enter ShapeArabic.
func TestBoundaryArabicPathUnchanged(t *testing.T) {
	// Path with Arabic letters: "مسار/ملف.go"
	// U+0645 U+0633 U+0627 U+0631 / U+0645 U+0644 U+0641 . g o
	text := "\u0645\u0633\u0627\u0631/\u0645\u0644\u0641.go"
	spans := []Span{
		{Start: 0, End: len(text), Kind: Path, StyleID: 0},
	}

	lines := renderShaped(t, text, spans, 80, Policy{Mode: Logical, Base: RTL})
	clusters := clustersOf(lines)

	// In Path span, Arabic letters must remain their original logical unshaped codepoints!
	wantRunes := []rune(text)
	var gotRunes []rune
	for _, c := range clusters {
		gotRunes = append(gotRunes, []rune(c.Text)...)
	}

	if len(gotRunes) != len(wantRunes) {
		t.Fatalf("Path got %d runes, want %d", len(gotRunes), len(wantRunes))
	}
	for i := range wantRunes {
		if gotRunes[i] != wantRunes[i] {
			t.Fatalf("Path rune %d: got %U, want %U (Path must remain unshaped)", i, gotRunes[i], wantRunes[i])
		}
	}

	// Verify Kind is Path on all runs
	for _, r := range lines[0].Runs {
		if r.Kind != Path {
			t.Fatalf("run Kind = %v, want Path", r.Kind)
		}
	}
}

// TestBoundaryArabicURLUnchanged proves that URL spans are LTR islands and NEVER enter ShapeArabic.
func TestBoundaryArabicURLUnchanged(t *testing.T) {
	text := "https://example.com/\u0645\u0631\u062D\u0628\u0627"
	spans := []Span{
		{Start: 0, End: len(text), Kind: URL, StyleID: 0},
	}

	lines := renderShaped(t, text, spans, 80, Policy{Mode: Logical, Base: RTL})
	clusters := clustersOf(lines)

	wantRunes := []rune(text)
	var gotRunes []rune
	for _, c := range clusters {
		gotRunes = append(gotRunes, []rune(c.Text)...)
	}

	if len(gotRunes) != len(wantRunes) {
		t.Fatalf("URL got %d runes, want %d", len(gotRunes), len(wantRunes))
	}
	for i := range wantRunes {
		if gotRunes[i] != wantRunes[i] {
			t.Fatalf("URL rune %d: got %U, want %U (URL must remain unshaped)", i, gotRunes[i], wantRunes[i])
		}
	}

	for _, r := range lines[0].Runs {
		if r.Kind != URL {
			t.Fatalf("run Kind = %v, want URL", r.Kind)
		}
	}
}

// TestBoundaryArabicCommandUnchanged proves that Command spans are LTR islands and NEVER enter ShapeArabic.
func TestBoundaryArabicCommandUnchanged(t *testing.T) {
	text := "\u0627\u0645\u0631-\u062A\u062C\u0631\u0628\u0629"
	spans := []Span{
		{Start: 0, End: len(text), Kind: Command, StyleID: 0},
	}

	lines := renderShaped(t, text, spans, 80, Policy{Mode: Logical, Base: RTL})
	clusters := clustersOf(lines)

	wantRunes := []rune(text)
	var gotRunes []rune
	for _, c := range clusters {
		gotRunes = append(gotRunes, []rune(c.Text)...)
	}

	if len(gotRunes) != len(wantRunes) {
		t.Fatalf("Command got %d runes, want %d", len(gotRunes), len(wantRunes))
	}
	for i := range wantRunes {
		if gotRunes[i] != wantRunes[i] {
			t.Fatalf("Command rune %d: got %U, want %U (Command must remain unshaped)", i, gotRunes[i], wantRunes[i])
		}
	}

	for _, r := range lines[0].Runs {
		if r.Kind != Command {
			t.Fatalf("run Kind = %v, want Command", r.Kind)
		}
	}
}

// TestBoundaryArabicStyleBoundary proves that runs do NOT cross Style boundaries.
func TestBoundaryArabicStyleBoundary(t *testing.T) {
	// "ك" (Kaf, StyleID 10) + "تب" (Teh, Beh, StyleID 20) = "كتب"
	text := "\u0643\u062A\u0628"
	spans := []Span{
		{Start: 0, End: 2, Kind: Prose, StyleID: 10},
		{Start: 2, End: 6, Kind: Prose, StyleID: 20},
	}

	lines := renderShaped(t, text, spans, 80, Policy{Mode: Logical, Base: RTL})
	clusters := clustersOf(lines)
	if len(clusters) != 3 {
		t.Fatalf("got %d clusters, want 3", len(clusters))
	}

	// Because of StyleID boundary:
	// Run 0 has "ك" alone -> must be ISOLATED Kaf (0xFED9), NOT initial (0xFEDB)!
	// Run 1 has "تب" -> Teh has no preceding neighbor in Run 1 -> must be INITIAL Teh (0xFE97), NOT medial (0xFE98)!
	// Beh has preceding Teh in Run 1 -> FINAL Beh (0xFE90).
	wantRune0 := rune(0xFED9) // Kaf isolated
	wantRune1 := rune(0xFE97) // Teh initial
	wantRune2 := rune(0xFE90) // Beh final

	if got := []rune(clusters[0].Text); len(got) != 1 || got[0] != wantRune0 {
		t.Fatalf("cluster 0: got %U, want %U (must be isolated, proving no style-crossing)", got, wantRune0)
	}
	if got := []rune(clusters[1].Text); len(got) != 1 || got[0] != wantRune1 {
		t.Fatalf("cluster 1: got %U, want %U (must be initial, proving no style-crossing)", got, wantRune1)
	}
	if got := []rune(clusters[2].Text); len(got) != 1 || got[0] != wantRune2 {
		t.Fatalf("cluster 2: got %U, want %U", got, wantRune2)
	}

	// Verify source ranges
	if clusters[0].SrcBytes != [2]int{0, 2} {
		t.Fatalf("cluster 0 SrcBytes = %v, want [0, 2]", clusters[0].SrcBytes)
	}
	if clusters[1].SrcBytes != [2]int{2, 4} {
		t.Fatalf("cluster 1 SrcBytes = %v, want [2, 4]", clusters[1].SrcBytes)
	}
	if clusters[2].SrcBytes != [2]int{4, 6} {
		t.Fatalf("cluster 2 SrcBytes = %v, want [4, 6]", clusters[2].SrcBytes)
	}

	restored, err := RestoreFromSource(text, clusters)
	if err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}
	if restored != text {
		t.Fatalf("restored = %q, want %q", restored, text)
	}
}

// TestBoundaryArabicSpanBoundary proves that runs do NOT cross Span boundaries
// even when StyleID is identical.
func TestBoundaryArabicSpanBoundary(t *testing.T) {
	// "ك" (Span 0, StyleID 0) + "تب" (Span 1, StyleID 0)
	text := "\u0643\u062A\u0628"
	spans := []Span{
		{Start: 0, End: 2, Kind: Prose, StyleID: 0},
		{Start: 2, End: 6, Kind: Prose, StyleID: 0},
	}

	lines := renderShaped(t, text, spans, 80, Policy{Mode: Logical, Base: RTL})
	clusters := clustersOf(lines)
	if len(clusters) != 3 {
		t.Fatalf("got %d clusters, want 3", len(clusters))
	}

	// Kaf must be isolated (0xFED9) because Span 0 is a separate run.
	// Teh must be initial (0xFE97) because Span 1 is a separate run.
	if got := []rune(clusters[0].Text); len(got) != 1 || got[0] != 0xFED9 {
		t.Fatalf("cluster 0: got %U, want 0xFED9 (isolated)", got)
	}
	if got := []rune(clusters[1].Text); len(got) != 1 || got[0] != 0xFE97 {
		t.Fatalf("cluster 1: got %U, want 0xFE97 (initial)", got)
	}
	if got := []rune(clusters[2].Text); len(got) != 1 || got[0] != 0xFE90 {
		t.Fatalf("cluster 2: got %U, want 0xFE90 (final)", got)
	}
}

// TestBoundaryArabicLevelBoundary proves that runs do NOT cross BiDi level boundaries.
func TestBoundaryArabicLevelBoundary(t *testing.T) {
	// Text: "كتب 123" (Kaf, Teh, Beh, space, 1, 2, 3) in RTL paragraph.
	// "كتب" has level 1 (Arabic). "123" has level 2 (European number).
	text := "\u0643\u062A\u0628 123"
	lines := renderShaped(t, text, nil, 80, Policy{Mode: Logical, Base: RTL})

	clusters := clustersOf(lines)
	if len(clusters) != 7 {
		t.Fatalf("got %d clusters, want 7", len(clusters))
	}

	// "كتب" shaped as initial Kaf, medial Teh, final Beh:
	want := []rune{0xFEDB, 0xFE98, 0xFE90, ' ', '1', '2', '3'}
	for i, wantR := range want {
		if got := []rune(clusters[i].Text); len(got) != 1 || got[0] != wantR {
			t.Fatalf("cluster %d: got %U, want %U", i, got, wantR)
		}
	}
}

// TestBoundaryArabicLineBoundary proves that runs do NOT cross hard line breaks.
func TestBoundaryArabicLineBoundary(t *testing.T) {
	// Line 1: "ك" (Kaf)
	// Line 2: "تب" (Teh, Beh)
	text := "\u0643\n\u062A\u0628"
	lines := renderShaped(t, text, nil, 80, Policy{Mode: Logical, Base: RTL})

	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}

	line1Clusters := clustersOf([]VisualLine{lines[0]})
	line2Clusters := clustersOf([]VisualLine{lines[1]})

	// Line 1: Kaf isolated (0xFED9)
	if len(line1Clusters) != 1 || []rune(line1Clusters[0].Text)[0] != 0xFED9 {
		t.Fatalf("line 1: got %v, want [0xFED9]", line1Clusters)
	}

	// Line 2: Teh initial (0xFE97), Beh final (0xFE90)
	if len(line2Clusters) != 2 ||
		[]rune(line2Clusters[0].Text)[0] != 0xFE97 ||
		[]rune(line2Clusters[1].Text)[0] != 0xFE90 {
		t.Fatalf("line 2: got %v, want [0xFE97, 0xFE90]", line2Clusters)
	}
}

// TestBoundaryLamAlefBoundary proves that Lam-Alef ligature does NOT form across
// a style, span, or semantic boundary, and DOES form when within the same run.
func TestBoundaryLamAlefBoundary(t *testing.T) {
	t.Run("Lam and Alef separated by Span boundary do NOT form ligature", func(t *testing.T) {
		// "ل" (Lam, Span 0) + "ا" (Alef, Span 1)
		text := "\u0644\u0627"
		spans := []Span{
			{Start: 0, End: 2, Kind: Prose, StyleID: 1},
			{Start: 2, End: 4, Kind: Prose, StyleID: 2},
		}

		lines := renderShaped(t, text, spans, 80, Policy{Mode: Logical, Base: RTL})
		clusters := clustersOf(lines)
		if len(clusters) != 2 {
			t.Fatalf("got %d clusters, want 2 (must not merge into ligature)", len(clusters))
		}

		// Lam must be isolated Lam (0xFEDD), NOT Lam-Alef (0xFEFB)
		if got := []rune(clusters[0].Text); len(got) != 1 || got[0] != 0xFEDD {
			t.Fatalf("cluster 0: got %U, want 0xFEDD (isolated Lam)", got)
		}
		// Alef must be isolated Alef (0xFE8D), NOT Lam-Alef
		if got := []rune(clusters[1].Text); len(got) != 1 || got[0] != 0xFE8D {
			t.Fatalf("cluster 1: got %U, want 0xFE8D (isolated Alef)", got)
		}
	})

	t.Run("Lam and Alef in same run DO form ligature", func(t *testing.T) {
		text := "\u0644\u0627"
		lines := renderShaped(t, text, nil, 80, Policy{Mode: Logical, Base: RTL})

		clusters := clustersOf(lines)
		if len(clusters) != 1 {
			t.Fatalf("got %d clusters, want 1 (ligature)", len(clusters))
		}

		// Hand-authored expected: isolated Lam-Alef ligature (0xFEFB)
		if got := []rune(clusters[0].Text); len(got) != 1 || got[0] != 0xFEFB {
			t.Fatalf("cluster 0: got %U, want 0xFEFB", got)
		}
		if clusters[0].SrcBytes != [2]int{0, 4} {
			t.Fatalf("SrcBytes = %v, want [0, 4]", clusters[0].SrcBytes)
		}
		if clusters[0].SrcRunes != [2]int{0, 2} {
			t.Fatalf("SrcRunes = %v, want [0, 2]", clusters[0].SrcRunes)
		}

		restored, err := RestoreFromSource(text, clusters)
		if err != nil {
			t.Fatalf("RestoreFromSource: %v", err)
		}
		if restored != text {
			t.Fatalf("restored = %q, want %q", restored, text)
		}
	})

	t.Run("Lam + transparent mark + Alef separated by boundary do NOT form ligature", func(t *testing.T) {
		// "لَ" (Lam + Fatha, Span 0) + "ا" (Alef, Span 1)
		text := "\u0644\u064E\u0627"
		spans := []Span{
			{Start: 0, End: 4, Kind: Prose, StyleID: 1},
			{Start: 4, End: 6, Kind: Prose, StyleID: 2},
		}

		lines := renderShaped(t, text, spans, 80, Policy{Mode: Logical, Base: RTL})
		clusters := clustersOf(lines)
		if len(clusters) != 2 {
			t.Fatalf("got %d clusters, want 2", len(clusters))
		}

		// Cluster 0: Lam isolated (0xFEDD) + Fatha (0x064E)
		if got := []rune(clusters[0].Text); len(got) != 2 || got[0] != 0xFEDD || got[1] != 0x064E {
			t.Fatalf("cluster 0: got %U, want [0xFEDD, 0x064E]", got)
		}
		// Cluster 1: Alef isolated (0xFE8D)
		if got := []rune(clusters[1].Text); len(got) != 1 || got[0] != 0xFE8D {
			t.Fatalf("cluster 1: got %U, want [0xFE8D]", got)
		}
	})

	t.Run("Lam + transparent mark + Alef in same run DO form ligature with mark", func(t *testing.T) {
		// "لَا" (Lam + Fatha + Alef)
		text := "\u0644\u064E\u0627"
		lines := renderShaped(t, text, nil, 80, Policy{Mode: Logical, Base: RTL})

		clusters := clustersOf(lines)
		if len(clusters) != 1 {
			t.Fatalf("got %d clusters, want 1", len(clusters))
		}

		// Ligature with Fatha: [0xFEFB, 0x064E]
		if got := []rune(clusters[0].Text); len(got) != 2 || got[0] != 0xFEFB || got[1] != 0x064E {
			t.Fatalf("cluster 0: got %U, want [0xFEFB, 0x064E]", got)
		}
		if clusters[0].SrcBytes != [2]int{0, 6} {
			t.Fatalf("SrcBytes = %v, want [0, 6]", clusters[0].SrcBytes)
		}
		if clusters[0].SrcRunes != [2]int{0, 3} {
			t.Fatalf("SrcRunes = %v, want [0, 3]", clusters[0].SrcRunes)
		}

		restored, err := RestoreFromSource(text, clusters)
		if err != nil {
			t.Fatalf("RestoreFromSource: %v", err)
		}
		if restored != text {
			t.Fatalf("restored = %q, want %q", restored, text)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Source Contiguity Assertion Before Shaping
// ─────────────────────────────────────────────────────────────────────────────

// TestRunClustersAreSourceContiguousBeforeShaping explicitly proves that
// clusters within each run are contiguous in source byte and rune offsets.
func TestRunClustersAreSourceContiguousBeforeShaping(t *testing.T) {
	texts := []string{
		"مرحبا بالعالم",
		"كتب 123 درس",
		"لَا تَقُلْ ذَلِكَ",
		"كلمة1 كلمة2 كلمة3 كلمة4",
		benchMixed,
	}

	for _, txt := range texts {
		lines := renderShaped(t, txt, nil, 40, Policy{Mode: ReorderAndMirror, Base: Auto})
		for li, line := range lines {
			for ri, run := range line.Runs {
				for ci := 0; ci < len(run.Clusters)-1; ci++ {
					c1 := run.Clusters[ci]
					c2 := run.Clusters[ci+1]
					// Within visual runs of same direction:
					if run.Level%2 == 0 {
						if c1.SrcBytes[1] != c2.SrcBytes[0] {
							t.Fatalf("line %d run %d LTR: cluster %d end %d != cluster %d start %d",
								li, ri, ci, c1.SrcBytes[1], ci+1, c2.SrcBytes[0])
						}
					} else {
						// In RTL runs after L2 reordering, clusters are reversed:
						if c2.SrcBytes[1] != c1.SrcBytes[0] {
							t.Fatalf("line %d run %d RTL: cluster %d start %d != cluster %d end %d",
								li, ri, ci, c1.SrcBytes[0], ci+1, c2.SrcBytes[1])
						}
					}
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Width Contracts Under Shaping
// ─────────────────────────────────────────────────────────────────────────────

// TestVisualWidthWithinRequestedWidth tests that every visual line satisfies
// line.Width <= requestedWidth across widths: 1, 2, 3, 7, 12, 20, 39, 40.
func TestVisualWidthWithinRequestedWidth(t *testing.T) {
	testWidths := []int{1, 2, 3, 7, 12, 20, 39, 40}

	testPayloads := []struct {
		name string
		text string
	}{
		{
			name: "Lam-Alef variants",
			text: "لا لَآ لَأ لَإ لَا الإسلام للأمة هؤلاء",
		},
		{
			name: "Multiple marks / diacritics",
			text: "بِسْمِ اللَّهِ الرَّحْمَٰنِ الرَّحِيمِ كَتَبَ يُكْتَبُ مَكْتُوبٌ",
		},
		{
			name: "Mixed Arabic and Latin text",
			text: "مرحبا feed.go بالعالم test ./... النتيجة 123 نجاح",
		},
		{
			name: "Single Arabic word",
			text: "قسطنطينية",
		},
	}

	for _, w := range testWidths {
		for _, p := range testPayloads {
			lines := renderShaped(t, p.text, nil, w, Policy{Mode: ReorderAndMirror, Base: Auto})
			for li, l := range lines {
				// Documented exception: if a single cluster exceeds width (e.g. width=1 for 2-column or indivisible cluster),
				// it cannot be broken further.
				if len(clustersOf([]VisualLine{l})) == 1 && clustersOf([]VisualLine{l})[0].Width > w {
					continue
				}
				if l.Width > w {
					t.Fatalf("width=%d payload %q line %d: width %d > limit %d",
						w, p.name, li, l.Width, w)
				}
				if l.Width < 0 {
					t.Fatalf("width=%d payload %q line %d: negative width %d",
						w, p.name, li, l.Width)
				}
			}

			// Also verify lossless source restoration
			restored, err := RestoreFromSource(p.text, clustersOf(lines))
			if err != nil {
				t.Fatalf("width=%d payload %q: RestoreFromSource failed: %v", w, p.name, err)
			}
			wantText := strings.ReplaceAll(p.text, "\n", "")
			if restored != wantText {
				t.Fatalf("width=%d payload %q: restore mismatch\ngot  %q\nwant %q",
					w, p.name, restored, wantText)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. Source Restoration After L2 Visual Reordering
// ─────────────────────────────────────────────────────────────────────────────

// TestSourceRestorationAfterL2VisualReordering proves that RestoreFromSource
// completely and losslessly reconstructs the logical text even after visual
// L2 reordering and L4 glyph mirroring.
func TestSourceRestorationAfterL2VisualReordering(t *testing.T) {
	inputs := []string{
		"كتب (سلام) [123] عالم",
		"مرحبا hello (test)! و النتيجة (100%)",
		"لا إله إلا الله محمد رسول الله",
		"مسار/ملف.go مع الأمر `git status` و النتيجة: نجاح",
		benchMixed,
	}

	for _, in := range inputs {
		for _, mode := range []Mode{Logical, Reorder, ReorderAndMirror} {
			lines := renderShaped(t, in, nil, 30, Policy{Mode: mode, Base: Auto})
			restored, err := RestoreFromSource(in, clustersOf(lines))
			if err != nil {
				t.Fatalf("Mode=%v RestoreFromSource: %v", mode, err)
			}
			want := strings.ReplaceAll(in, "\n", "")
			if restored != want {
				t.Fatalf("Mode=%v restore failed:\ngot  %q\nwant %q", mode, restored, want)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 6. Marks Association After Visual Reordering
// ─────────────────────────────────────────────────────────────────────────────

// TestMarksRemainAssociatedAfterVisualReordering proves that transparent marks
// (diacritics) remain attached to their base letter in visual clusters after L2 reordering.
func TestMarksRemainAssociatedAfterVisualReordering(t *testing.T) {
	// Base with multiple marks: Beh + Shadda + Fatha ("بَّ")
	// U+0628 U+0651 U+064E
	text := "\u0628\u0651\u064E"

	lines := renderShaped(t, text, nil, 80, Policy{Mode: ReorderAndMirror, Base: RTL})
	clusters := clustersOf(lines)

	if len(clusters) != 1 {
		t.Fatalf("got %d clusters, want exactly 1 cluster for base+marks", len(clusters))
	}

	runes := []rune(clusters[0].Text)
	// Base letter must be shaped (isolated Beh 0xFE8F), followed by Shadda and Fatha
	if len(runes) != 3 {
		t.Fatalf("cluster text has %d runes, want 3", len(runes))
	}
	if runes[0] != 0xFE8F {
		t.Fatalf("base rune = %U, want 0xFE8F (isolated Beh)", runes[0])
	}
	if runes[1] != 0x0651 {
		t.Fatalf("mark 1 = %U, want 0x0651 (Shadda)", runes[1])
	}
	if runes[2] != 0x064E {
		t.Fatalf("mark 2 = %U, want 0x064E (Fatha)", runes[2])
	}

	// Verify reversibility
	restored, err := RestoreFromSource(text, clusters)
	if err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}
	if restored != text {
		t.Fatalf("restored = %q, want %q", restored, text)
	}
}
