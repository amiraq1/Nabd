package rtl

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"

	"nabd/internal/rtl/bidi"
)

// Mode selects how much visual processing Layout applies.
//
// PR 1 implements exactly these steps: bidi levels (UAX #9 through rule L2),
// cluster-safe rule L3 (grapheme clusters are the only reordering unit; raw
// runes are never reversed), and rule L4 glyph mirroring. Arabic cursive
// shaping is NOT implemented. A ShapingMode decision — after measuring Termux
// rendering to avoid double shaping — is required before Layout is wired into
// the feed, and PR 2 must not treat ReorderAndMirror as a complete Arabic
// solution. See provenance.md, "Arabic shaping gate for PR 2".
type Mode uint8

const (
	// Logical returns clusters in logical order: levels are resolved but no
	// rule L2 reordering and no mirroring are applied.
	Logical Mode = iota
	// Reorder applies rule L2 at grapheme-cluster granularity; no mirroring.
	Reorder
	// ReorderAndMirror applies rule L2 plus rule L4 glyph mirroring. It does
	// not perform Arabic shaping: no joining forms, no presentation forms, no
	// GSUB. See the Mode documentation above and provenance.md.
	ReorderAndMirror
)

// SpanKind classifies a caller-supplied semantic unit. Code, Path, URL and
// Command spans are LTR islands: they keep an even embedding level and are
// never broken by the wrapper. Prose spans carry style metadata only; prose
// gaps between spans behave like Prose with StyleID 0.
type SpanKind uint8

const (
	Prose SpanKind = iota
	Code
	Path
	URL
	// Command is reserved for the semantic Markdown layer (PR 2).
	Command
)

// Span is one semantic unit over the logical text. Start and End are byte
// offsets, half-open [Start, End), into the string passed to Layout. Spans are
// validated strictly (sorted, non-overlapping, non-empty, inside the input,
// aligned to UTF-8 rune and grapheme-cluster boundaries); see Layout.
type Span struct {
	Start   int
	End     int
	Kind    SpanKind
	StyleID uint16
}

// Direction selects the paragraph base direction for Layout.
type Direction uint8

const (
	// Auto applies rules P2/P3 per line.
	Auto Direction = iota
	// LTR forces paragraph level 0.
	LTR
	// RTL forces paragraph level 1.
	RTL
)

// Policy controls one Layout call. Shaping is deliberately absent: it will be
// a separate policy after the ShapingMode decision (see provenance.md).
type Policy struct {
	Mode Mode
	Base Direction
}

// Run is a maximal sequence of visually adjacent clusters sharing the same
// level, span kind and style. Runs merge only on these visible attributes;
// they are not split by internal span identity, and the per-cluster source
// ranges carry that identity for callers.
type Run struct {
	Clusters []Cluster
	Level    uint8
	Kind     SpanKind
	StyleID  uint16
}

// VisualLine is one wrapped, reordered line.
type VisualLine struct {
	Runs  []Run
	Width int
}

// Errors returned by Layout. Callers fall back to safe logical rendering.
var (
	ErrInvalidWidth  = errors.New("rtl: width must be >= 1")
	ErrInvalidPolicy = errors.New("rtl: invalid policy")
	// ErrInvalidSpans reports a malformed span slice; errors.Is matches it.
	ErrInvalidSpans = errors.New("rtl: invalid spans")
)

// classifiedCluster is a grapheme cluster annotated with its semantic span.
//
// SpanID identifies the atom: two adjacent spans never merge into one unit,
// not even when they share a Kind, and a span never merges with a differently
// kinded neighbour. Clusters outside every span have SpanID -1 and are prose
// gaps — they are never LTR islands and never inherit atomicity from a
// neighbouring span.
type classifiedCluster struct {
	Cluster
	Kind    SpanKind
	StyleID uint16
	SpanID  int
}

// layoutOptions controls internal unexported features of Layout.
type layoutOptions struct {
	arabicShaping bool
}

// Layout runs the display pipeline over already-sanitized logical text with
// caller-supplied semantic spans:
//
//	spans -> provisional logical wrap -> grapheme clusters ->
//	canonical bracket pairing (bidi engine) -> levels -> L1/L2 at cluster
//	granularity -> L3 (clusters never split) -> mirroring ->
//	final width measurement
//
// Spans must be:
//   - sorted ascending by Start;
//   - non-overlapping;
//   - non-empty: Start < End;
//   - inside [0, len(logical)];
//   - aligned to UTF-8 rune boundaries and extended grapheme-cluster
//     boundaries.
//
// Any violation returns an error matching ErrInvalidSpans, never a panic.
// Layout never modifies logical or spans.
//
// A nil or empty span slice means the whole text is prose with StyleID 0.
// Gaps between spans are prose with StyleID 0 and are breakable; every span
// is one atom (its clusters are never merged with neighbours for wrapping or
// attribution, even when the next span has the same Kind), and spans with
// Kind != Prose additionally keep an even, LTR-island embedding level. Spans
// covering whitespace are allowed when a caller (the Markdown layer in PR 2)
// emits them explicitly. Binding "\n" separates lines; each line is an
// independent paragraph. The returned clusters carry absolute source ranges
// into logical.
func Layout(logical string, spans []Span, width int, policy Policy) ([]VisualLine, error) {
	return layoutWithOptions(logical, spans, width, policy, layoutOptions{})
}

// layoutWithOptions is an internal unexported entry point used by package tests
// to exercise shaping features without global mutable state.
func layoutWithOptions(logical string, spans []Span, width int, policy Policy, opts layoutOptions) ([]VisualLine, error) {
	if width < 1 {
		return nil, ErrInvalidWidth
	}
	if policy.Mode > ReorderAndMirror || policy.Base > RTL {
		return nil, ErrInvalidPolicy
	}

	clusters := clusterize(logical)
	if err := validateSpans(logical, spans, clusters); err != nil {
		return nil, err
	}
	classified := classifyClusters(clusters, spans)

	// Split into lines on '\n' clusters (CRLF counts as one cluster).
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

		// ── paragraph-level analysis ────────────────────────────────────────
		// Collect the logical rune sequence for the whole paragraph line.
		var paraRunes []rune
		for _, cc := range line {
			paraRunes = append(paraRunes, []rune(cc.Text)...)
		}

		// ASCII fast-path decision at paragraph level (not per-piece).
		paraBase := -1
		switch policy.Base {
		case LTR:
			paraBase = 0
		case RTL:
			paraBase = 1
		}
		asciiPara := policy.Base != RTL && isSimpleASCII(string(paraRunes))

		// Compute wrapped-piece cluster boundaries.
		pieces := splitPieces(line, width)

		// Build rune-index linebreaks for AnalyzeWithLineBreaks.
		// We need the rune count up to each piece boundary (cluster index).
		// Cluster ci in line[] spans runeWidths[ci] runes starting from runeStart[ci].
		runeStart := make([]int, len(line)+1)
		for i, cc := range line {
			runeStart[i+1] = runeStart[i] + len([]rune(cc.Text))
		}
		// For each piece, the linebreak is the rune index of the first cluster
		// of the *next* piece (or len(paraRunes) for the last piece).
		linebreaks := make([]int, len(pieces))
		for pi := range pieces {
			if pi < len(pieces)-1 {
				// First cluster of next piece.
				nextStart := pieces[pi+1][0]
				linebreaks[pi] = runeStart[nextStart]
			} else {
				linebreaks[pi] = len(paraRunes)
			}
		}

		// Analyse the paragraph once. pieceLevels[i] holds per-rune levels
		// for piece i after rule L1.
		var paraAnalysis bidi.Analysis
		var pieceLevels [][]uint8
		if !asciiPara {
			var err error
			paraAnalysis, pieceLevels, err = bidi.AnalyzeWithLineBreaks(paraRunes, paraBase, linebreaks)
			if err != nil {
				return nil, err
			}
		}

		for pi, piece := range pieces {
			var pLevels []uint8
			if !asciiPara {
				pLevels = pieceLevels[pi]
			}
			vl, err := buildLine(logical, line, piece, policy, paraAnalysis.ParaLevel, pLevels, runeStart, asciiPara, opts)
			if err != nil {
				return nil, err
			}
			out = append(out, vl)
		}
	}
	return out, nil
}

// buildLine reorders and measures one wrapped piece using pre-computed
// paragraph levels. When opts.arabicShaping is false, it routes
// directly to buildLineBaseline with zero overhead.
func buildLine(logical string, line []classifiedCluster, piece []int, policy Policy, paraLevel uint8, pieceLevels []uint8, runeStart []int, asciiPara bool, opts layoutOptions) (VisualLine, error) {
	if !opts.arabicShaping {
		return buildLineBaseline(line, piece, policy, paraLevel, pieceLevels, runeStart, asciiPara)
	}
	return buildLineShaped(logical, line, piece, policy, paraLevel, pieceLevels, runeStart, asciiPara, opts)
}

func buildLineBaseline(line []classifiedCluster, piece []int, policy Policy, paraLevel uint8, pieceLevels []uint8, runeStart []int, asciiPara bool) (VisualLine, error) {
	levels := make([]uint8, len(piece))
	order := make([]int, len(piece))
	for i := range order {
		order[i] = i
	}

	if !asciiPara {
		// Derive per-cluster level from the paragraph analysis: take the level
		// of the first rune of the cluster within this piece.
		//
		// runeStart[piece[ci]] is the absolute rune index of cluster piece[ci]
		// in the paragraph; pieceLevels is indexed from the start of the piece,
		// so subtract runeStart[piece[0]] to get the piece-relative index.
		pieceRuneBase := runeStart[piece[0]]
		for ci, li := range piece {
			relIdx := runeStart[li] - pieceRuneBase
			levels[ci] = pieceLevels[relIdx]
		}

		// LTR islands keep an even embedding level (rule L2 invariant).
		even := paraLevel
		if even%2 == 1 {
			even++
		}
		for ci, li := range piece {
			if line[li].Kind != Prose {
				levels[ci] = even
			}
		}
		if policy.Mode != Logical {
			order = l2Reorder(levels)
		}
	}

	vis := make([]Cluster, 0, len(piece))
	visLevels := make([]uint8, 0, len(piece))
	visKinds := make([]SpanKind, 0, len(piece))
	visStyles := make([]uint16, 0, len(piece))
	for _, ci := range order {
		li := piece[ci]
		c := line[li].Cluster
		if policy.Mode == ReorderAndMirror && line[li].Kind == Prose && levels[ci]%2 == 1 {
			mirrored := mirrorText(c.Text)
			if mirrored != c.Text {
				c.Text = mirrored
				c.Width = uniseg.StringWidth(mirrored)
			}
		}
		vis = append(vis, c)
		visLevels = append(visLevels, levels[ci])
		visKinds = append(visKinds, line[li].Kind)
		visStyles = append(visStyles, line[li].StyleID)
	}

	var runs []Run
	for i := range vis {
		last := len(runs) - 1
		if len(runs) == 0 || runs[last].Level != visLevels[i] ||
			runs[last].Kind != visKinds[i] || runs[last].StyleID != visStyles[i] {
			runs = append(runs, Run{Level: visLevels[i], Kind: visKinds[i], StyleID: visStyles[i]})
			last = len(runs) - 1
		}
		runs[last].Clusters = append(runs[last].Clusters, vis[i])
	}
	return VisualLine{Runs: runs, Width: clusterWidths(vis)}, nil
}

func buildLineShaped(logical string, line []classifiedCluster, piece []int, policy Policy, paraLevel uint8, pieceLevels []uint8, runeStart []int, asciiPara bool, opts layoutOptions) (VisualLine, error) {
	levels := make([]uint8, len(piece))
	if !asciiPara {
		pieceRuneBase := runeStart[piece[0]]
		for ci, li := range piece {
			relIdx := runeStart[li] - pieceRuneBase
			levels[ci] = pieceLevels[relIdx]
		}

		even := paraLevel
		if even%2 == 1 {
			even++
		}
		for ci, li := range piece {
			if line[li].Kind != Prose {
				levels[ci] = even
			}
		}
	}

	// Partition line into eligible runs.
	// Boundary rules: start a new run at first difference in:
	// - prose eligibility (Kind == Prose)
	// - StyleID
	// - SpanID / semantic owner
	// - compatible BiDi level
	// - hard newline
	type runRange struct {
		start int
		end   int
	}
	var inlineRuns [8]runRange
	var logicalRuns []runRange
	if len(piece) <= 8 {
		logicalRuns = inlineRuns[:0]
	} else {
		logicalRuns = make([]runRange, 0, len(piece))
	}

	runStart := 0
	for i := 1; i < len(piece); i++ {
		prevLi := piece[i-1]
		currLi := piece[i]

		prevProse := line[prevLi].Kind == Prose
		currProse := line[currLi].Kind == Prose

		if prevProse != currProse ||
			line[prevLi].Kind != line[currLi].Kind ||
			line[prevLi].StyleID != line[currLi].StyleID ||
			line[prevLi].SpanID != line[currLi].SpanID ||
			levels[i-1] != levels[i] ||
			line[prevLi].Cluster.SrcBytes[1] != line[currLi].Cluster.SrcBytes[0] ||
			line[prevLi].Cluster.SrcRunes[1] != line[currLi].Cluster.SrcRunes[0] ||
			isHardBreakCluster(line[prevLi].Text) ||
			isHardBreakCluster(line[currLi].Text) {
			logicalRuns = append(logicalRuns, runRange{start: runStart, end: i})
			runStart = i
		}
	}
	if len(piece) > 0 {
		logicalRuns = append(logicalRuns, runRange{start: runStart, end: len(piece)})
	}

	var inlineItems [128]shapedItem
	var items []shapedItem
	if len(piece) <= 128 {
		items = inlineItems[:0]
	} else {
		items = make([]shapedItem, 0, len(piece))
	}

	for _, r := range logicalRuns {
		firstLi := piece[r.start]
		kind := line[firstLi].Kind
		style := line[firstLi].StyleID
		level := levels[r.start]

		// Ineligible runs (Code, Path, URL, Command) MUST NEVER enter ShapeArabic.
		if kind != Prose {
			for k := r.start; k < r.end; k++ {
				li := piece[k]
				items = append(items, shapedItem{
					c:     line[li].Cluster,
					level: levels[k],
					kind:  line[li].Kind,
					style: line[li].StyleID,
				})
			}
			continue
		}

		runByteStart := line[piece[r.start]].Cluster.SrcBytes[0]
		runByteEnd := line[piece[r.end-1]].Cluster.SrcBytes[1]
		textStr := logical[runByteStart:runByteEnd]

		// Non-Arabic prose runs do not undergo Arabic cursive shaping.
		if !hasArabicScript(textStr) {
			for k := r.start; k < r.end; k++ {
				li := piece[k]
				items = append(items, shapedItem{
					c:     line[li].Cluster,
					level: levels[k],
					kind:  line[li].Kind,
					style: line[li].StyleID,
				})
			}
			continue
		}

		// Invariant: run clusters must be strictly source-contiguous before shaping.
		for k := r.start; k < r.end-1; k++ {
			c1 := line[piece[k]].Cluster
			c2 := line[piece[k+1]].Cluster
			if c1.SrcBytes[1] != c2.SrcBytes[0] || c1.SrcRunes[1] != c2.SrcRunes[0] {
				return VisualLine{}, fmt.Errorf("rtl: shaped run is not source-contiguous at cluster %d", k)
			}
		}

		// Eligible Arabic Prose run -> shapeArabicRun.
		runRuneStart := line[piece[r.start]].Cluster.SrcRunes[0]
		items = shapeArabicRun(textStr, runByteStart, runRuneStart, level, kind, style, items)
	}

	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	if policy.Mode != Logical && !asciiPara {
		var inlineLevels [128]uint8
		var itemLevels []uint8
		if len(items) <= 128 {
			itemLevels = inlineLevels[:len(items)]
		} else {
			itemLevels = make([]uint8, len(items))
		}
		for i := range items {
			itemLevels[i] = items[i].level
		}
		order = l2Reorder(itemLevels)
	}

	var visualRuns []Run
	width := 0
	for _, ci := range order {
		it := items[ci]
		c := it.c
		lvl := it.level
		k := it.kind
		st := it.style
		if policy.Mode == ReorderAndMirror && k == Prose && lvl%2 == 1 {
			mirrored := mirrorText(c.Text)
			if mirrored != c.Text {
				c.Text = mirrored
				c.Width = uniseg.StringWidth(mirrored)
			}
		}
		width += c.Width

		last := len(visualRuns) - 1
		if len(visualRuns) == 0 || visualRuns[last].Level != lvl ||
			visualRuns[last].Kind != k || visualRuns[last].StyleID != st {
			visualRuns = append(visualRuns, Run{Level: lvl, Kind: k, StyleID: st})
			last = len(visualRuns) - 1
		}
		visualRuns[last].Clusters = append(visualRuns[last].Clusters, c)
	}
	return VisualLine{Runs: visualRuns, Width: width}, nil
}

func hasArabicScript(s string) bool {
	hasArabicByte := false
	for i := 0; i < len(s); i++ {
		if uint8(s[i]) >= 0xD8 {
			hasArabicByte = true
			break
		}
	}
	if !hasArabicByte {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 0x0600 && r <= 0x06FF,
			r >= 0x0750 && r <= 0x077F,
			r >= 0x0870 && r <= 0x08FF,
			r >= 0xFB50 && r <= 0xFDFF,
			r >= 0xFE70 && r <= 0xFEFF,
			r >= 0x10EC0 && r <= 0x10EFF:
			return true
		}
	}
	return false
}

// isHardBreakCluster reports whether a cluster text contains a hard line break.
// The byte scan is equivalent to ContainsAny(s, "\r\n") for UTF-8 input:
// 0x0A/0x0D bytes cannot occur inside multi-byte sequences, so rune decoding
// is unnecessary (profiled hotspot: strings.IndexAny via ContainsAny).
func isHardBreakCluster(s string) bool {
	return strings.IndexByte(s, '\n') >= 0 || strings.IndexByte(s, '\r') >= 0
}

// validateSpans enforces the documented span contract. The returned error
// always wraps ErrInvalidSpans.
func validateSpans(logical string, spans []Span, clusters []Cluster) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidSpans, fmt.Sprintf(format, args...))
	}

	total := len(logical)
	for i, s := range spans {
		switch {
		case s.Start < 0 || s.End < 0:
			return invalid("span %d has a negative offset: [%d, %d)", i, s.Start, s.End)
		case s.Start >= s.End:
			return invalid("span %d is empty or reversed: [%d, %d)", i, s.Start, s.End)
		case s.End > total:
			return invalid("span %d ends beyond the input: %d > %d", i, s.End, total)
		case i > 0 && s.Start < spans[i-1].Start:
			return invalid("span %d is out of order: starts at %d after %d", i, s.Start, spans[i-1].Start)
		case i > 0 && s.Start < spans[i-1].End:
			return invalid("span %d overlaps span %d", i, i-1)
		}
	}
	clusterStarts := make(map[int]bool, len(clusters))
	for _, c := range clusters {
		clusterStarts[c.SrcBytes[0]] = true
	}
	for i, s := range spans {
		if s.Start < total && !utf8.RuneStart(logical[s.Start]) {
			return invalid("span %d starts inside a UTF-8 rune", i)
		}
		if s.End < total && !utf8.RuneStart(logical[s.End]) {
			return invalid("span %d ends inside a UTF-8 rune", i)
		}
		if !clusterStarts[s.Start] {
			return invalid("span %d starts inside a grapheme cluster", i)
		}
		if s.End < total && !clusterStarts[s.End] {
			return invalid("span %d ends inside a grapheme cluster", i)
		}
	}
	return nil
}

// classifyClusters attributes every cluster to its span (or to a prose gap).
// It does not modify clusters or spans.
func classifyClusters(clusters []Cluster, spans []Span) []classifiedCluster {
	out := make([]classifiedCluster, len(clusters))
	si := 0
	for i, c := range clusters {
		cc := classifiedCluster{Cluster: c, SpanID: -1}
		for si < len(spans) && spans[si].End <= c.SrcBytes[0] {
			si++
		}
		if si < len(spans) && spans[si].Start <= c.SrcBytes[0] && c.SrcBytes[1] <= spans[si].End {
			cc.Kind = spans[si].Kind
			cc.StyleID = spans[si].StyleID
			cc.SpanID = si
		}
		out[i] = cc
	}
	return out
}

// splitPieces performs the provisional logical wrap. Boundaries are cluster
// indices. Break opportunities are prose spaces; a span (atom) stays intact
// unless it alone exceeds the width, in which case it is split at grapheme
// cluster boundaries (documented overflow fallback).
func splitPieces(line []classifiedCluster, limit int) [][]int {
	n := len(line)
	var pieces [][]int
	start := 0
	for start < n {
		fit := start
		w := 0
		for fit < n {
			cw := line[fit].Width
			if fit > start && w+cw > limit {
				break
			}
			w += cw
			fit++
		}
		end := fit
		if end < n {
			as, ae := atomBounds(line, end-1)
			switch {
			case ae > end:
				// The cut falls inside an atom.
				if as > start {
					end = as
				} else {
					end = fit // the atom starts the line and does not fit
				}
			case isGap(line[end-1]) && isGap(line[end]) &&
				!isSpaceText(line[end-1].Text) && !isSpaceText(line[end].Text):
				// Mid-word cut in prose: backtrack to the last space.
				cut := -1
				for k := end - 1; k >= start; k-- {
					if isGap(line[k]) && line[k].Text == " " {
						cut = k + 1
						break
					}
				}
				if cut > start {
					end = cut
				}
			}
		}
		if end <= start {
			end = start + 1
		}
		pieces = append(pieces, makeRange(start, end))
		start = end
	}
	return pieces
}

// atomBounds returns the cluster index range of the atom containing i: the
// span with i's SpanID, or the single cluster itself for a prose gap.
func atomBounds(line []classifiedCluster, i int) (int, int) {
	id := line[i].SpanID
	if id < 0 {
		return i, i + 1
	}
	start := i
	for start > 0 && line[start-1].SpanID == id {
		start--
	}
	end := i + 1
	for end < len(line) && line[end].SpanID == id {
		end++
	}
	return start, end
}

func isGap(c classifiedCluster) bool { return c.SpanID < 0 }

func isSpaceText(s string) bool { return s == " " }

func makeRange(start, end int) []int {
	out := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, i)
	}
	return out
}

// isSimpleASCII reports whether text is guaranteed to need no reordering in a
// non-RTL paragraph: pure ASCII without digits and without characters that
// flip under N0 or L4. Neutral punctuation between letters stays at the
// paragraph level, so such lines have level 0 everywhere and unit order.
func isSimpleASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b >= utf8.RuneSelf {
			return false
		}
		switch {
		case b >= '0' && b <= '9':
			return false
		case strings.IndexByte("()[]{}<>", b) >= 0:
			return false
		}
	}
	return true
}

// l2Reorder applies UBA rule L2 to cluster levels and returns the
// visual-to-logical permutation at cluster granularity.
func l2Reorder(levels []uint8) []int {
	result := make([]int, len(levels))
	for i := range result {
		result[i] = i
	}
	highest := uint8(0)
	lowestOdd := -1
	for _, l := range levels {
		if l > highest {
			highest = l
		}
		if l%2 == 1 && (lowestOdd == -1 || int(l) < lowestOdd) {
			lowestOdd = int(l)
		}
	}
	if lowestOdd == -1 {
		return result
	}
	for level := int(highest); level >= lowestOdd; level-- {
		for i := 0; i < len(levels); i++ {
			if int(levels[i]) >= level {
				start := i
				limit := i + 1
				for limit < len(levels) && int(levels[limit]) >= level {
					limit++
				}
				for j, k := start, limit-1; j < k; j, k = j+1, k-1 {
					result[j], result[k] = result[k], result[j]
				}
				i = limit
			}
		}
	}
	return result
}

// mirrorText mirrors every rune of s that has a Bidi_Mirroring entry.
func mirrorText(s string) string {
	needs := false
	for _, r := range s {
		if HasMirror(r) {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(Mirror(r))
	}
	return b.String()
}
