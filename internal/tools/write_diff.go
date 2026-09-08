package tools

// write_diff.go owns the unified-diff machinery: the LCS matrix, its work
// budget, hunk assembly, and the "\ No newline at end of file" handling.
// Moved out of write.go without behaviour changes.

import (
	"context"
	"fmt"
	"strings"
)

// NBD-011: configurable bounds for diff work and output. They are vars (not
// const) so tests can inject small budgets without constructing huge fixtures,
// and so production limits can be tuned after G1 measurement (Task 7). The
// values below are the safety-reviewed starting ceilings.
var (
	// maxDiffLines caps each side of the diff; inputs larger than this are not
	// fed to the LCS matrix. 3000 lines is far above typical single edits.
	maxDiffLines = 3000
	// maxDiffCells caps the matrix work (n*m). If n*m would exceed it, the diff
	// aborts rather than allocate quadratic state. 4M cells * 8 B/int (arm64)
	// = 32 MB, calibrated AT the G1 measurement: a 2000x2000 diff allocated
	// 33.6 MB and ran in ~24 ms. The ceiling is set at the measured worst case
	// (0x margin), not below it: 2000*2000 == 4_000_000 exactly. The (n*m)
	// guard `m > maxDiffCells/n` therefore rejects 2001x2000 and larger
	// BEFORE allocating. Larger edits are rejected before alloc; this ceiling
	// is the deterministic boundary, not a soft target.
	maxDiffCells = 4_000_000
	// maxPatchBytes caps the raw unified-diff output string. A 2000-line
	// complete rewrite is well under 100 KB; 1 MB gives >10x headroom.
	maxPatchBytes = 1 << 20
)

// unifiedDiff renders before→after as a unified diff. It is line-based and
// minimal: unchanged lines are shared context. A nil before means creation.
//
// NBD-011: unifiedDiff now respects a work budget (maxDiffLines, maxDiffCells)
// and ctx cancellation. If the inputs are too large or the context is cancelled,
// it returns an error and no patch. The caller (buildRecord) must persist the
// record without the patch so hashes and blobs survive. Hunk headers track the
// new-file line position independently so the patch is syntactically valid.
func unifiedDiff(ctx context.Context, before, after []byte, path string) (string, error) {
	// Check cancellation before doing any work.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	bl := splitLines(before)
	al := splitLines(after)

	n, m := len(bl.lines), len(al.lines)
	// Bound the input sides.
	if n > maxDiffLines || m > maxDiffLines {
		return "", fmt.Errorf("diff input too large: %d×%d lines exceeds %d", n, m, maxDiffLines)
	}
	// Bound the matrix work and detect integer overflow in n*m. If the product
	// would overflow int or exceed the cell budget, abort before allocating.
	if n != 0 && m > maxDiffCells/n {
		return "", fmt.Errorf("diff work budget exceeded: %d×%d exceeds %d cells", n, m, maxDiffCells)
	}
	// LCS table for the two line sequences. Cancellation is checked once per
	// outer iteration so a cancelled context terminates the build promptly.
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		for j := m - 1; j >= 0; j-- {
			if bl.lines[i] == al.lines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				if lcs[i+1][j] >= lcs[i][j+1] {
					lcs[i][j] = lcs[i+1][j]
				} else {
					lcs[i][j] = lcs[i][j+1]
				}
			}
		}
	}

	// Backtrack the LCS into an edit script: ('=',line) unchanged,
	// ('-',line) deleted, ('+',line) inserted. Cancellation is checked during
	// the backtrack so a cancelled context terminates it.
	type op struct {
		kind byte
		line string
	}
	ops := make([]op, 0, n+m)
	i, j := n, m
	for i > 0 || j > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i > 0 && j > 0 && bl.lines[i-1] == al.lines[j-1] {
			ops = append(ops, op{'=', bl.lines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i-1][j] >= lcs[i][j-1]) {
			ops = append(ops, op{'+', al.lines[j-1]})
			j--
		} else {
			ops = append(ops, op{'-', bl.lines[i-1]})
			i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}

	const ctxSize = 3
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	oldLine, newLine := 1, 1
	type hunk struct {
		oldStart, newStart int
		lines              []string
	}
	var hunks []hunk
	cur := -1
	startHunk := func() {
		if cur < 0 {
			hunks = append(hunks, hunk{})
			cur = len(hunks) - 1
		}
	}
	var pending []string
	flushPendingTo := func(idx int) {
		if len(pending) == 0 {
			return
		}
		hunks[idx].lines = append(hunks[idx].lines, pending...)
		pending = nil
	}
	for _, o := range ops {
		switch o.kind {
		case '=':
			if cur < 0 {
				if len(pending) < ctxSize {
					pending = append(pending, " "+o.line)
				}
				oldLine++
				newLine++
				continue
			}
			pending = append(pending, " "+o.line)
			oldLine++
			newLine++
			if len(pending) > ctxSize {
				hunks[cur].lines = append(hunks[cur].lines, pending[:ctxSize]...)
				cur = -1
				pending = nil
			}
		case '-':
			if cur < 0 && len(pending) > 0 {
				startHunk()
				hunks[cur].oldStart = oldLine - len(pending)
				hunks[cur].newStart = newLine - len(pending)
				flushPendingTo(cur)
			}
			startHunk()
			if len(hunks[cur].lines) == 0 && hunks[cur].oldStart == 0 {
				hunks[cur].oldStart = oldLine
				hunks[cur].newStart = newLine
			}
			hunks[cur].lines = append(hunks[cur].lines, "-"+o.line)
			oldLine++
		case '+':
			if cur < 0 && len(pending) > 0 {
				startHunk()
				hunks[cur].oldStart = oldLine - len(pending)
				hunks[cur].newStart = newLine - len(pending)
				flushPendingTo(cur)
			}
			startHunk()
			if len(hunks[cur].lines) == 0 && hunks[cur].oldStart == 0 {
				hunks[cur].oldStart = oldLine
				hunks[cur].newStart = newLine
			}
			hunks[cur].lines = append(hunks[cur].lines, "+"+o.line)
			newLine++
		}
	}
	if cur >= 0 {
		if len(pending) > ctxSize {
			pending = pending[len(pending)-ctxSize:]
		}
		if len(pending) > 0 {
			hunks[cur].lines = append(hunks[cur].lines, pending...)
		}
	}

	// Emit "\ No newline at end of file" markers for files that lack a trailing
	// newline. The marker follows the last line from each affected side in the
	// last hunk, so git apply round-trips byte-for-byte (NBD-011 Task 5).
	if len(hunks) > 0 && (bl.noNL || al.noNL) {
		last := &hunks[len(hunks)-1]
		oldNoNL := bl.noNL && len(bl.lines) > 0
		newNoNL := al.noNL && len(al.lines) > 0
		// If the last line of one file is a context line shared with the other
		// file, but the NL statuses differ, split the context line into
		// delete + add so the \ No newline marker lands on the correct side.
		if oldNoNL != newNoNL {
			oIdx, nIdx := lastOldNewIdx(last.lines)
			if oIdx >= 0 && oIdx == nIdx {
				// Shared context line is the last for both sides.
				content := last.lines[oIdx][1:]
				last.lines = spliceContext(last.lines, oIdx, content)
			} else if oIdx >= 0 && nIdx >= 0 && oIdx != nIdx {
				// Last old and new lines differ; check for a shared context
				// line at the end of one side that lacks NL while the other
				// side has NL.
				if last.lines[oIdx][0] == ' ' && oldNoNL && !newNoNL {
					content := last.lines[oIdx][1:]
					last.lines = spliceContext(last.lines, oIdx, content)
				} else if last.lines[nIdx][0] == ' ' && newNoNL && !oldNoNL {
					content := last.lines[nIdx][1:]
					last.lines = spliceContext(last.lines, nIdx, content)
				}
			}
		}
		// Find the last old and new line indices (after potential split).
		lastOldIdx, lastNewIdx := lastOldNewIdx(last.lines)
		var marked []string
		for i, l := range last.lines {
			marked = append(marked, l)
			isLastOld := i == lastOldIdx && oldNoNL
			isLastNew := i == lastNewIdx && newNoNL
			if isLastOld && isLastNew && lastOldIdx == lastNewIdx {
				// Same line ends both files; one marker suffices.
				marked = append(marked, "\\ No newline at end of file")
			} else {
				if isLastOld {
					marked = append(marked, "\\ No newline at end of file")
				}
				if isLastNew {
					marked = append(marked, "\\ No newline at end of file")
				}
			}
		}
		last.lines = marked
	}

	for _, h := range hunks {
		oldCnt, newCnt := 0, 0
		for _, l := range h.lines {
			switch l[0] {
			case '-':
				oldCnt++
			case '+':
				newCnt++
			case '\\':
				// \ No newline at end of file marker — not a content line.
			default:
				oldCnt++
				newCnt++
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.oldStart, oldCnt, h.newStart, newCnt)
		for _, l := range h.lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	out := b.String()
	if len(out) > maxPatchBytes {
		return "", fmt.Errorf("patch output exceeds %d bytes", maxPatchBytes)
	}
	return out, nil
}

// lineSet is the result of splitting bytes into lines, tracking whether the
// original data lacked a trailing newline. splitLines strips the trailing
// newline before splitting, so the noNL flag is needed to emit the
// "\ No newline at end of file" marker in unifiedDiff.
type lineSet struct {
	lines []string
	noNL  bool // true if the original data did not end with '\n'
}

func splitLines(b []byte) lineSet {
	if len(b) == 0 {
		return lineSet{}
	}
	noNL := !strings.HasSuffix(string(b), "\n")
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return lineSet{noNL: noNL}
	}
	return lineSet{lines: strings.Split(s, "\n"), noNL: noNL}
}

// lastOldNewIdx returns the indices of the last old-side line ('-'/ ' ') and
// the last new-side line ('+'/ ' ') in the hunk lines.
func lastOldNewIdx(lines []string) (oldIdx, newIdx int) {
	oldIdx, newIdx = -1, -1
	for i, l := range lines {
		switch l[0] {
		case '-':
			oldIdx = i
		case '+':
			newIdx = i
		case ' ':
			oldIdx = i
			newIdx = i
		}
	}
	return
}

// spliceContext replaces the context line at index idx (prefixed with ' ')
// with a delete + add pair, preserving the line content. This is used to
// split shared context lines when the old and new sides have different
// trailing-newline status, so that "\ No newline at end of file" markers
// can be placed on the correct side.
func spliceContext(lines []string, idx int, content string) []string {
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:idx]...)
	result = append(result, "-"+content, "+"+content)
	result = append(result, lines[idx+1:]...)
	return result
}
