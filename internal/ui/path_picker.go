package ui

// The @ path picker for the Feed composer.
//
// Structure mirrors slashMenu on purpose: same open/close/next/prev shape,
// same two-border popup, same width modes. A second, differently-behaving
// popup in the same composer would be a worse outcome than a little
// repetition.
//
// The index comes from pathindex.Scan, which is the walk measured in #110,
// #111 and #112 and promoted in #113. Two consequences are load-bearing:
//
//   - Scan refuses symlinked entries and skips .ag, so a path the picker can
//     offer is always a real regular file inside the root. The picker adds no
//     path of its own and never widens that set.
//   - Scan stops at the first limit it trips. A truncated index is a partial
//     file list, and the user cannot tell by looking, so the popup border says
//     so rather than pretending the list is the repository.

import (
	"path"
	"sort"
	"strings"

	"nabd/internal/pathindex"
	"nabd/internal/tools"

	"github.com/charmbracelet/x/ansi"
)

// pickerMaxItems bounds the candidate list handed to the popup. It is a
// display bound, not a security bound: containment is already decided by
// Scan.
const pickerMaxItems = 50

// pathIndex is the picker's view of a scan: the paths plus whether they are
// all of them.
type pathIndex struct {
	paths []string
	// complete is false when Scan stopped at a limit. The popup must say so.
	complete bool
	// stop is kept for the status line and for tests that assert which limit
	// truncated the index.
	stop pathindex.StopReason
}

// scanPathIndex walks dir once and returns the index the picker completes
// against. Errors are the caller's to surface; an unreadable root yields an
// empty index rather than a partial one presented as whole.
func scanPathIndex(dir string) (pathIndex, error) {
	root, err := tools.NewRoot(dir)
	if err != nil {
		return pathIndex{}, err
	}
	idx := pathindex.Scan(root, pathindex.Config{})
	return pathIndex{paths: idx.Paths, complete: idx.Complete(), stop: idx.Stop}, nil
}

// atToken is an unfinished @path reference in the composer: the index of the
// '@' itself and the text typed after it.
type atToken struct {
	start int
	query string
}

// findAtToken returns the @token the composer text currently ends with.
//
// Completion works on the end of the text rather than an arbitrary cursor
// position because the composer exposes the cursor only as a logical line
// number (see composer.cursorLogicalLine). Mid-text completion needs a rune
// offset the textarea does not publish today; until it does, offering it
// would complete the wrong token.
//
// A token is only recognised when the '@' opens a word — at the start of the
// text or after whitespace — so an email address or a Go build tag is never
// mistaken for a path reference.
func findAtToken(text string) (atToken, bool) {
	i := strings.LastIndexByte(text, '@')
	if i < 0 {
		return atToken{}, false
	}
	if i > 0 {
		prev := text[i-1]
		if prev != ' ' && prev != '\t' && prev != '\n' {
			return atToken{}, false
		}
	}
	query := text[i+1:]
	if strings.ContainsAny(query, " \t\n") {
		return atToken{}, false
	}
	return atToken{start: i, query: query}, true
}

// matchPaths ranks the index against a query and returns at most limit
// candidates.
//
// Ranking, best first:
//
//  1. the file name starts with the query — what a user typing "sca" means
//  2. the file name contains it
//  3. any other part of the path contains it
//
// Matching is case-insensitive. Ties break on the path itself, so the same
// query against the same index always produces the same list: the ordering is
// part of what makes the popup safe to drive with Tab.
func matchPaths(paths []string, query string, limit int) []string {
	if limit <= 0 {
		limit = pickerMaxItems
	}
	if query == "" {
		if len(paths) <= limit {
			out := make([]string, len(paths))
			copy(out, paths)
			return out
		}
		out := make([]string, limit)
		copy(out, paths[:limit])
		return out
	}

	q := strings.ToLower(query)
	type scored struct {
		rank int
		p    string
	}
	var hits []scored
	for _, p := range paths {
		lower := strings.ToLower(p)
		base := strings.ToLower(path.Base(p))
		switch {
		case strings.HasPrefix(base, q):
			hits = append(hits, scored{0, p})
		case strings.Contains(base, q):
			hits = append(hits, scored{1, p})
		case strings.Contains(lower, q):
			hits = append(hits, scored{2, p})
		}
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].rank != hits[b].rank {
			return hits[a].rank < hits[b].rank
		}
		return hits[a].p < hits[b].p
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.p)
	}
	return out
}

// completeAtToken replaces the token with the chosen path and leaves a
// trailing space, so the next character typed starts a new word instead of
// extending the path.
func completeAtToken(text string, tok atToken, p string) string {
	if tok.start < 0 || tok.start > len(text) {
		return text
	}
	return text[:tok.start] + "@" + p + " "
}

// pathPicker is the popup state.
type pathPicker struct {
	visible  bool
	items    []string
	selected int
	// token is the reference being completed, captured when the popup opened.
	token atToken
	// partial mirrors pathIndex.complete: the popup says when the index it is
	// completing against is not the whole tree.
	partial bool
}

func newPathPicker() *pathPicker { return &pathPicker{} }

func (p *pathPicker) open(items []string, tok atToken, partial bool) {
	p.visible = true
	p.items = items
	p.token = tok
	p.partial = partial
	if p.selected >= len(items) || p.selected < 0 {
		p.selected = 0
	}
}

func (p *pathPicker) close() {
	p.visible = false
	p.items = nil
	p.selected = 0
	p.token = atToken{}
	p.partial = false
}

func (p *pathPicker) next() {
	if len(p.items) > 0 {
		p.selected = (p.selected + 1) % len(p.items)
	}
}

func (p *pathPicker) prev() {
	if len(p.items) > 0 {
		p.selected = (p.selected - 1 + len(p.items)) % len(p.items)
	}
}

// currentPath returns the highlighted path.
func (p *pathPicker) currentPath() (string, bool) {
	if !p.visible || len(p.items) == 0 || p.selected < 0 || p.selected >= len(p.items) {
		return "", false
	}
	return p.items[p.selected], true
}

// shape reuses the slash menu's row policy: two border rows plus items,
// clamped to maxRows and never below menuMinRows, with the window centred on
// the selection.
func (p *pathPicker) shape(maxRows ...int) slashMenuShape {
	if !p.visible || len(p.items) == 0 {
		return slashMenuShape{}
	}
	full, rows := len(p.items)+2, len(p.items)+2
	if len(maxRows) > 0 && maxRows[0] > 0 {
		rows = maxRows[0]
	}
	if rows > full {
		rows = full
	}
	if rows < menuMinRows {
		rows = menuMinRows
	}
	itemRows, start := rows-2, 0
	if itemRows > 0 && len(p.items) > itemRows {
		start = p.selected - itemRows/2
		if start < 0 {
			start = 0
		}
		if start+itemRows > len(p.items) {
			start = len(p.items) - itemRows
		}
	}
	return slashMenuShape{rows: rows, start: start, end: min(start+itemRows, len(p.items))}
}

func (p *pathPicker) lineCount(maxRows ...int) int { return p.shape(maxRows...).rows }

// view renders the popup. The header is the disclosure surface: when the
// index was truncated it says "partial index" instead of letting a clipped
// file list pass for the repository.
func (p *pathPicker) view(width int, maxRows ...int) string {
	if !p.visible || len(p.items) == 0 {
		return ""
	}
	w := width
	if w < 20 {
		w = 20
	}
	mode, menuW := widthMode(w), w
	if mode != WidthWide && menuW > 50 {
		menuW = 50
	}
	header := "── Files "
	if p.partial {
		header = "── Files (partial index) "
		if ansi.StringWidth(header) > menuW {
			header = "── Files (partial) "
		}
	}
	dashes := menuW - ansi.StringWidth(header)
	if dashes < 0 {
		dashes = 0
	}
	shape := p.shape(maxRows...)
	var b strings.Builder
	b.WriteString(dim.Render(header + strings.Repeat("─", dashes)))
	b.WriteByte('\n')
	for i := shape.start; i < shape.end; i++ {
		line, prefix := p.items[i], "  "
		if ansi.StringWidth(line) > menuW-2 {
			// Paths are truncated from the left: the file name is what
			// identifies the row, and it lives at the end.
			line = "…" + ansi.Truncate(reverseRunes(line), menuW-3, "")
			line = "…" + reverseRunes(strings.TrimPrefix(line, "…"))
		}
		if i == p.selected {
			prefix = "> "
			b.WriteString(good.Render(prefix + line))
		} else {
			b.WriteString(dim.Render(prefix + line))
		}
		b.WriteByte('\n')
	}
	b.WriteString(dim.Render(strings.Repeat("─", menuW)))
	return b.String()
}

// reverseRunes reverses a string by runes. Used only to truncate a path from
// the left while keeping multi-byte characters intact.
func reverseRunes(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
