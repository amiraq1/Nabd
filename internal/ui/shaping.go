package ui

import (
	"strings"

	"nabd/internal/rtl"
)

// shapeProseRuns shapes the prose runs of an already-parsed logical line,
// jointly across style boundaries, and rebuilds the spans in visual-offset
// space (Step 1 — shape after Markdown; see
// docs/rtl-step1-shape-after-markdown.md).
//
// Every span with Kind != rtl.Prose is an LTR island — Code today; Path, URL
// and Command are reserved kinds — and is copied verbatim: never shaped, its
// bytes preserved, its kind and style carried into the rebuilt span. Prose
// regions between islands are shaped as one run, so joining and the Lam-Alef
// ligature cross style boundaries. Each shaped cluster takes the StyleID of
// the span covering its first source byte; adjacent styled clusters merge
// into one Prose span, and plain clusters stay gaps (no span) so the wrapper
// can keep breaking prose at spaces (memo decision D2).
//
// The returned spans are sorted, non-overlapping, bounded by the returned
// visual string, and every island keeps its bytes literally.
func shapeProseRuns(logical string, spans []rtl.Span) (string, []rtl.Span) {
	var b strings.Builder
	b.Grow(len(logical) + 16)
	var out []rtl.Span
	vpos := 0

	// shapeProse shapes one prose region and emits its styled spans.
	shapeProse := func(start, end int) {
		if start >= end {
			return
		}
		clusters := rtl.ShapeArabic(logical[start:end])
		if len(clusters) == 0 {
			// Engine contract: non-empty input yields clusters. Copy
			// verbatim rather than dropping text if that ever fails.
			b.WriteString(logical[start:end])
			vpos += end - start
			return
		}
		si := 0
		for si < len(spans) && spans[si].End <= start {
			si++
		}
		runStart := -1
		var runStyle uint16
		flush := func() {
			if runStart >= 0 {
				out = append(out, rtl.Span{Start: runStart, End: vpos, Kind: rtl.Prose, StyleID: runStyle})
				runStart = -1
			}
		}
		for _, c := range clusters {
			p := start + c.SourceStart
			for si < len(spans) && spans[si].End <= p {
				si++
			}
			style := styleNone
			if si < len(spans) && spans[si].Start <= p {
				style = spans[si].StyleID
			}
			switch {
			case style == styleNone:
				flush()
			case runStart < 0:
				runStart, runStyle = vpos, style
			case runStyle != style:
				flush()
				runStart, runStyle = vpos, style
			}
			visual := string(c.Visual)
			b.WriteString(visual)
			vpos += len(visual)
		}
		flush()
	}

	pos := 0
	for _, s := range spans {
		if s.Kind == rtl.Prose {
			continue
		}
		shapeProse(pos, s.Start)
		island := logical[s.Start:s.End]
		out = append(out, rtl.Span{Start: vpos, End: vpos + len(island), Kind: s.Kind, StyleID: s.StyleID})
		b.WriteString(island)
		vpos += len(island)
		pos = s.End
	}
	shapeProse(pos, len(logical))
	return b.String(), out
}
