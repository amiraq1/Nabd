// Package rtl is Nabd's internal right-to-left text engine. It owns the
// cluster-safe reordering pipeline used at the display boundary and isolates
// the temporary fork of x/text's bidi core behind a small API
// (see provenance.md). Nothing in this package depends on the UI layer.
package rtl

// mirrorPair is one Bidi_Mirrored glyph mapping.
type mirrorPair struct {
	from rune
	to   rune
}

// Mirror returns the glyph mirror of r under rule L4, or r itself when the
// character has no Bidi_Mirroring.txt entry.
func Mirror(r rune) rune {
	lo, hi := 0, len(mirrorPairs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		switch {
		case mirrorPairs[mid].from < r:
			lo = mid + 1
		case mirrorPairs[mid].from > r:
			hi = mid
		default:
			return mirrorPairs[mid].to
		}
	}
	return r
}

// HasMirror reports whether r has a glyph mirror.
func HasMirror(r rune) bool { return Mirror(r) != r }

// mirrorEntryCount is exposed for the conformance gate.
func mirrorEntryCount() int { return len(mirrorPairs) }
