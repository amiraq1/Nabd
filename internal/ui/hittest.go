package ui

// hittest.go provides line-to-item mapping. It is pure lookup: it never
// renders, never mutates model state, and never touches the journal.

// shiftOffsets rebases per-item start offsets after boundRenderedLines
// dropped trimmed leading rows, so offsets and m.lines always live in the
// same coordinate space.
//
// Items whose rows were dropped entirely collapse to offset 0. They are no
// longer separately addressable in the viewport, which is exactly what the
// retention cap means; the journal remains the full source of truth. The
// result stays non-decreasing, so itemAt's binary search remains valid.
func shiftOffsets(offsets []int, trimmed int) []int {
	if trimmed <= 0 || len(offsets) == 0 {
		out := make([]int, len(offsets))
		copy(out, offsets)
		return out
	}
	out := make([]int, len(offsets))
	for i, o := range offsets {
		if o <= trimmed {
			out[i] = 0
		} else {
			out[i] = o - trimmed
		}
	}
	return out
}

func (m *Feed) itemAt(line int) int {
	if line < 0 || line >= len(m.lines) {
		return -1
	}
	lo, hi := 0, len(m.offsets)-1
	ans := -1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if m.offsets[mid] <= line {
			ans = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return ans
}
