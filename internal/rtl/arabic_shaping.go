package rtl

import (
	"unicode/utf8"
)

// stackRunes is the decode-buffer size for stack-allocated rune slices.
// Compared 64/128/256 via BenchmarkStackRuns: 256 keeps inputs up to 256
// bytes on the stack (inputs in (128,256] bytes lose a ~8 KiB heap allocation
// per call); 64 falls off the stack path above 64 bytes. 256 retained.
const stackRunes = 256

// ShapeCluster represents a shaped visual cluster with its original logical
// source byte offsets into the input text.
//
// Invariants:
//   - 0 <= SourceStart < SourceEnd <= len(text)
//   - SourceStart and SourceEnd align to valid UTF-8 rune boundaries.
//   - text[SourceStart:SourceEnd] exactly reconstructs the logical source slice.
//   - When concatenating all clusters in order, they partition the input text:
//     clusters[i].SourceEnd == clusters[i+1].SourceStart.
//   - Visual contains the shaped Presentation Forms-B runes (or logical runes
//     when presentation forms are unavailable) and attached diacritics.
type ShapeCluster struct {
	SourceStart int // byte offset
	SourceEnd   int // byte offset, exclusive
	Visual      []rune
}

// textRune tracks a decoded rune with its byte offsets and Joining_Type.
type textRune struct {
	r         rune
	byteStart int
	byteEnd   int
	jt        joiningClass
}

// canJoinLeft reports whether a character with joining class jt can connect
// to a following (left) character in logical order.
func canJoinLeft(jt joiningClass) bool {
	return jt == joiningClassDual || jt == joiningClassLeft || jt == joiningClassCausing
}

// canJoinRight reports whether a character with joining class jt can connect
// to a preceding (right) character in logical order.
func canJoinRight(jt joiningClass) bool {
	return jt == joiningClassDual || jt == joiningClassRight || jt == joiningClassCausing
}

// prevCursiveNeighbor finds the nearest preceding non-transparent rune.
// Returns -1 if none exists or if a hard boundary (newline) is encountered.
func prevCursiveNeighbor(runes []textRune, idx int) int {
	for i := idx - 1; i >= 0; i-- {
		r := runes[i].r
		if r == '\n' || r == '\r' {
			return -1
		}
		if runes[i].jt != joiningClassTrans {
			return i
		}
	}
	return -1
}

// nextCursiveNeighbor finds the nearest following non-transparent rune.
// Returns -1 if none exists or if a hard boundary (newline) is encountered.
func nextCursiveNeighbor(runes []textRune, idx int) int {
	for i := idx + 1; i < len(runes); i++ {
		r := runes[i].r
		if r == '\n' || r == '\r' {
			return -1
		}
		if runes[i].jt != joiningClassTrans {
			return i
		}
	}
	return -1
}

// joinsRight reports whether runes[i] connects to its preceding cursive neighbor.
func joinsRight(runes []textRune, i int) bool {
	if !canJoinRight(runes[i].jt) {
		return false
	}
	prev := prevCursiveNeighbor(runes, i)
	if prev < 0 {
		return false
	}
	return canJoinLeft(runes[prev].jt)
}

// joinsLeft reports whether runes[i] connects to its following cursive neighbor.
func joinsLeft(runes []textRune, i int) bool {
	if !canJoinLeft(runes[i].jt) {
		return false
	}
	next := nextCursiveNeighbor(runes, i)
	if next < 0 {
		return false
	}
	return canJoinRight(runes[next].jt)
}

// ShapeArabic shapes an Arabic text string into a sequence of ShapeCluster elements.
//
// Design contracts:
//   - Pure standalone shaping engine: independent of layout, Policy, terminal width, or UI.
//   - Operates on logical text order.
//   - Cursive joining is resolved using Unicode Joining_Type properties.
//   - Transparent marks (Joining_Type=T) are skipped during neighbor search and
//     attached to their base letter in visual output.
//   - Lam-Alef pairs (0x0644 followed by an Alef variant 0x0622, 0x0623, 0x0625, 0x0627),
//     including any intervening or trailing transparent marks, are merged into a single
//     ShapeCluster.
//   - Logical Preservation Contract: when a presentation form glyph is unavailable
//     in Presentation Forms-B (e.g. U+0649 initial/medial, or unmapped extended Arabic),
//     the character is preserved as its logical rune without conversion.
//   - Hard boundaries: newlines (\n, \r) strictly break cursive connections.
//   - Source offsets are contiguous byte offsets into text, without gaps or overlaps.
//   - No normalization (NFC/NFD) is applied; original bytes are preserved.
func ShapeArabic(text string) []ShapeCluster {
	if len(text) == 0 {
		return nil
	}

	// 1. Decode text into runes with byte ranges and joining classes.
	// Use inline stack array for typical runs to eliminate heap allocation.
	var inlineRunes [stackRunes]textRune
	var runes []textRune
	if len(text) <= stackRunes {
		runes = inlineRunes[:0]
	} else {
		runes = make([]textRune, 0, len(text))
	}
	for byteOff := 0; byteOff < len(text); {
		r, sz := utf8.DecodeRuneInString(text[byteOff:])
		runes = append(runes, textRune{
			r:         r,
			byteStart: byteOff,
			byteEnd:   byteOff + sz,
			jt:        joiningType(r),
		})
		byteOff += sz
	}

	// 2. Iterate and shape clusters.
	clusters := make([]ShapeCluster, 0, len(runes))
	// Single backing array for all cluster visual runes to eliminate per-cluster slice allocations.
	visualStore := make([]rune, 0, len(runes)+8)
	n := len(runes)

	for i := 0; i < n; {
		curr := runes[i]

		// ── Hard boundaries (newline) ─────────────────────────────────────────
		if curr.r == '\n' || curr.r == '\r' {
			vStart := len(visualStore)
			visualStore = append(visualStore, curr.r)
			clusters = append(clusters, ShapeCluster{
				SourceStart: curr.byteStart,
				SourceEnd:   curr.byteEnd,
				Visual:      visualStore[vStart:len(visualStore):len(visualStore)],
			})
			i++
			continue
		}

		// ── Lam-Alef ligature detection ───────────────────────────────────────
		if curr.r == 0x0644 { // Lam
			nextNonTrans := nextCursiveNeighbor(runes, i)
			if nextNonTrans > 0 {
				alefRune := runes[nextNonTrans].r
				isoLig, finLig, isLamAlef := lamAlefFormOf(alefRune)
				if isLamAlef {
					// Choose isolated or final ligature form.
					// Lam-Alef never joins to the left (Alef is right-joining only).
					ligRune := isoLig
					if joinsRight(runes, i) {
						ligRune = finLig
					}

					// Collect intervening transparent marks between Lam and Alef,
					// and trailing transparent marks following the Alef.
					endIdx := nextNonTrans + 1
					for endIdx < n && runes[endIdx].jt == joiningClassTrans &&
						runes[endIdx].r != '\n' && runes[endIdx].r != '\r' {
						endIdx++
					}

					vStart := len(visualStore)
					visualStore = append(visualStore, ligRune)
					for k := i + 1; k < nextNonTrans; k++ {
						visualStore = append(visualStore, runes[k].r)
					}
					for k := nextNonTrans + 1; k < endIdx; k++ {
						visualStore = append(visualStore, runes[k].r)
					}

					clusters = append(clusters, ShapeCluster{
						SourceStart: curr.byteStart,
						SourceEnd:   runes[endIdx-1].byteEnd,
						Visual:      visualStore[vStart:len(visualStore):len(visualStore)],
					})
					i = endIdx
					continue
				}
			}
		}

		// ── Transparent marks without a preceding base letter ────────────────
		// (e.g. at the start of text, after spaces, or after non-Arabic characters)
		if curr.jt == joiningClassTrans {
			endIdx := i + 1
			for endIdx < n && runes[endIdx].jt == joiningClassTrans &&
				runes[endIdx].r != '\n' && runes[endIdx].r != '\r' {
				endIdx++
			}
			vStart := len(visualStore)
			for k := i; k < endIdx; k++ {
				visualStore = append(visualStore, runes[k].r)
			}
			clusters = append(clusters, ShapeCluster{
				SourceStart: curr.byteStart,
				SourceEnd:   runes[endIdx-1].byteEnd,
				Visual:      visualStore[vStart:len(visualStore):len(visualStore)],
			})
			i = endIdx
			continue
		}

		// ── Regular base character (Arabic or non-Arabic) ─────────────────────
		// Collect any following transparent marks attached to this base character.
		endIdx := i + 1
		for endIdx < n && runes[endIdx].jt == joiningClassTrans &&
			runes[endIdx].r != '\n' && runes[endIdx].r != '\r' {
			endIdx++
		}

		// Determine shaped visual rune for the base character.
		rightJ := joinsRight(runes, i)
		leftJ := joinsLeft(runes, i)
		shapedBase := shapedRune(curr.r, rightJ, leftJ)

		vStart := len(visualStore)
		visualStore = append(visualStore, shapedBase)
		for k := i + 1; k < endIdx; k++ {
			visualStore = append(visualStore, runes[k].r)
		}

		clusters = append(clusters, ShapeCluster{
			SourceStart: curr.byteStart,
			SourceEnd:   runes[endIdx-1].byteEnd,
			Visual:      visualStore[vStart:len(visualStore):len(visualStore)],
		})
		i = endIdx
	}

	return clusters
}

type shapedItem struct {
	c     Cluster
	level uint8
	kind  SpanKind
	style uint16
}

// shapingFastPathConfig controls whether fast paths and lookup tables are used in shapeArabicRun.
// An empty config (zero-value) has all fast paths and lookup tables enabled.
type shapingFastPathConfig struct {
	disableFastPaths bool
}

func markIndex(m rune) int {
	switch m {
	case 0x064B: // Fathatan
		return 0
	case 0x064C: // Dammatan
		return 1
	case 0x064D: // Kasratan
		return 2
	case 0x064E: // Fatha
		return 3
	case 0x064F: // Damma
		return 4
	case 0x0650: // Kasra
		return 5
	case 0x0651: // Shadda
		return 6
	case 0x0652: // Sukun
		return 7
	case 0x0670: // Dagger Alef
		return 8
	default:
		return -1
	}
}

// shapeArabicRun shapes text directly into items without intermediate heap allocations.
func shapeArabicRun(text string, runByteStart, runRuneStart int, level uint8, kind SpanKind, style uint16, items []shapedItem) []shapedItem {
	return shapeArabicRunWithConfig(text, runByteStart, runRuneStart, level, kind, style, items, shapingFastPathConfig{})
}

// shapeArabicRunWithConfig shapes text with configurable fast-path options.
func shapeArabicRunWithConfig(text string, runByteStart, runRuneStart int, level uint8, kind SpanKind, style uint16, items []shapedItem, cfg shapingFastPathConfig) []shapedItem {
	if len(text) == 0 {
		return items
	}

	fastEnabled := !cfg.disableFastPaths

	var inlineRunes [stackRunes]textRune
	var runes []textRune
	if fastEnabled && len(text) <= stackRunes {
		runes = inlineRunes[:0]
	} else {
		runes = make([]textRune, 0, len(text))
	}
	for byteOff := 0; byteOff < len(text); {
		r, sz := utf8.DecodeRuneInString(text[byteOff:])
		runes = append(runes, textRune{
			r:         r,
			byteStart: byteOff,
			byteEnd:   byteOff + sz,
			jt:        joiningType(r),
		})
		byteOff += sz
	}

	n := len(runes)
	currRune := runRuneStart
	var inlineVis [32]rune
	lastNonTrans := -1

	for i := 0; i < n; {
		curr := runes[i]

		// ── Hard boundaries (newline) ─────────────────────────────────────────
		if curr.r == '\n' || curr.r == '\r' {
			origByteStart := runByteStart + curr.byteStart
			origByteEnd := runByteStart + curr.byteEnd
			items = append(items, shapedItem{
				c: Cluster{
					Text:     text[curr.byteStart:curr.byteEnd],
					SrcRunes: [2]int{currRune, currRune + 1},
					SrcBytes: [2]int{origByteStart, origByteEnd},
					Width:    0,
				},
				level: level,
				kind:  kind,
				style: style,
			})
			currRune++
			lastNonTrans = -1
			i++
			continue
		}

		// ── Lam-Alef ligature detection ───────────────────────────────────────
		if curr.r == 0x0644 { // Lam
			nextNonTrans := i + 1
			for nextNonTrans < n && runes[nextNonTrans].jt == joiningClassTrans &&
				runes[nextNonTrans].r != '\n' && runes[nextNonTrans].r != '\r' {
				nextNonTrans++
			}
			if nextNonTrans < n && runes[nextNonTrans].r != '\n' && runes[nextNonTrans].r != '\r' {
				alefRune := runes[nextNonTrans].r
				isoLig, finLig, isLamAlef := lamAlefFormOf(alefRune)
				if isLamAlef {
					rightJ := false
					if fastEnabled {
						if lastNonTrans >= 0 && canJoinRight(curr.jt) && canJoinLeft(runes[lastNonTrans].jt) {
							rightJ = true
						}
					} else {
						rightJ = joinsRight(runes, i)
					}
					ligRune := isoLig
					if rightJ {
						ligRune = finLig
					}

					endIdx := nextNonTrans + 1
					for endIdx < n && runes[endIdx].jt == joiningClassTrans &&
						runes[endIdx].r != '\n' && runes[endIdx].r != '\r' {
						endIdx++
					}

					totalMarks := (nextNonTrans - (i + 1)) + (endIdx - (nextNonTrans + 1))
					var visText string
					if !fastEnabled {
						vis := make([]rune, 0, endIdx-i)
						vis = append(vis, ligRune)
						for k := i + 1; k < nextNonTrans; k++ {
							vis = append(vis, runes[k].r)
						}
						for k := nextNonTrans + 1; k < endIdx; k++ {
							vis = append(vis, runes[k].r)
						}
						visText = string(vis)
					} else if totalMarks == 0 {
						if ligRune >= 0xFE70 && ligRune <= 0xFEFF {
							visText = presFormStrings[ligRune-0xFE70]
						} else {
							visText = string(ligRune)
						}
					} else if totalMarks == 1 {
						var singleMark rune
						if nextNonTrans > i+1 {
							singleMark = runes[i+1].r
						} else {
							singleMark = runes[nextNonTrans+1].r
						}
						mi := markIndex(singleMark)
						if ligRune >= 0xFE70 && ligRune <= 0xFEFF && mi >= 0 {
							visText = presForm1MarkStrings[ligRune-0xFE70][mi]
						} else {
							visText = string([]rune{ligRune, singleMark})
						}
					} else {
						vis := inlineVis[:0]
						vis = append(vis, ligRune)
						for k := i + 1; k < nextNonTrans; k++ {
							vis = append(vis, runes[k].r)
						}
						for k := nextNonTrans + 1; k < endIdx; k++ {
							vis = append(vis, runes[k].r)
						}
						visText = string(vis)
					}

					numRunes := endIdx - i
					origByteStart := runByteStart + curr.byteStart
					origByteEnd := runByteStart + runes[endIdx-1].byteEnd
					items = append(items, shapedItem{
						c: Cluster{
							Text:     visText,
							SrcRunes: [2]int{currRune, currRune + numRunes},
							SrcBytes: [2]int{origByteStart, origByteEnd},
							Width:    1,
						},
						level: level,
						kind:  kind,
						style: style,
					})
					currRune += numRunes
					lastNonTrans = nextNonTrans
					i = endIdx
					continue
				}
			}
		}

		// ── Transparent marks without a preceding base letter ────────────────
		if curr.jt == joiningClassTrans {
			endIdx := i + 1
			for endIdx < n && runes[endIdx].jt == joiningClassTrans &&
				runes[endIdx].r != '\n' && runes[endIdx].r != '\r' {
				endIdx++
			}
			numRunes := endIdx - i
			origByteStart := runByteStart + curr.byteStart
			origByteEnd := runByteStart + runes[endIdx-1].byteEnd
			items = append(items, shapedItem{
				c: Cluster{
					Text:     text[curr.byteStart:runes[endIdx-1].byteEnd],
					SrcRunes: [2]int{currRune, currRune + numRunes},
					SrcBytes: [2]int{origByteStart, origByteEnd},
					Width:    0,
				},
				level: level,
				kind:  kind,
				style: style,
			})
			currRune += numRunes
			i = endIdx
			continue
		}

		// ── Regular base character (Arabic or non-Arabic) ─────────────────────
		endIdx := i + 1
		for endIdx < n && runes[endIdx].jt == joiningClassTrans &&
			runes[endIdx].r != '\n' && runes[endIdx].r != '\r' {
			endIdx++
		}

		rightJ := false
		if fastEnabled {
			if lastNonTrans >= 0 && canJoinRight(curr.jt) && canJoinLeft(runes[lastNonTrans].jt) {
				rightJ = true
			}
		} else {
			rightJ = joinsRight(runes, i)
		}

		leftJ := false
		if fastEnabled {
			if endIdx < n && runes[endIdx].r != '\n' && runes[endIdx].r != '\r' &&
				canJoinLeft(curr.jt) && canJoinRight(runes[endIdx].jt) {
				leftJ = true
			}
		} else {
			leftJ = joinsLeft(runes, i)
		}

		shapedBase := shapedRune(curr.r, rightJ, leftJ)
		isInvalidByte := curr.r == utf8.RuneError && (curr.byteEnd-curr.byteStart) == 1

		numMarks := endIdx - i - 1
		var visText string
		if !fastEnabled {
			if shapedBase == curr.r && !isInvalidByte {
				visText = text[curr.byteStart:runes[endIdx-1].byteEnd]
			} else {
				vis := make([]rune, 0, 1+numMarks)
				vis = append(vis, shapedBase)
				for k := i + 1; k < endIdx; k++ {
					vis = append(vis, runes[k].r)
				}
				visText = string(vis)
			}
		} else if numMarks == 0 {
			if shapedBase == curr.r {
				if isInvalidByte {
					visText = string(utf8.RuneError)
				} else {
					visText = text[curr.byteStart:curr.byteEnd]
				}
			} else if shapedBase >= 0xFE70 && shapedBase <= 0xFEFF {
				visText = presFormStrings[shapedBase-0xFE70]
			} else {
				visText = string(shapedBase)
			}
		} else if numMarks == 1 {
			m := runes[i+1].r
			if shapedBase == curr.r {
				if isInvalidByte {
					visText = string([]rune{shapedBase, m})
				} else {
					visText = text[curr.byteStart:runes[endIdx-1].byteEnd]
				}
			} else if mi := markIndex(m); shapedBase >= 0xFE70 && shapedBase <= 0xFEFF && mi >= 0 {
				visText = presForm1MarkStrings[shapedBase-0xFE70][mi]
			} else {
				visText = string([]rune{shapedBase, m})
			}
		} else {
			if shapedBase == curr.r && !isInvalidByte {
				visText = text[curr.byteStart:runes[endIdx-1].byteEnd]
			} else {
				vis := inlineVis[:0]
				vis = append(vis, shapedBase)
				for k := i + 1; k < endIdx; k++ {
					vis = append(vis, runes[k].r)
				}
				visText = string(vis)
			}
		}

		numRunes := endIdx - i
		origByteStart := runByteStart + curr.byteStart
		origByteEnd := runByteStart + runes[endIdx-1].byteEnd
		items = append(items, shapedItem{
			c: Cluster{
				Text:     visText,
				SrcRunes: [2]int{currRune, currRune + numRunes},
				SrcBytes: [2]int{origByteStart, origByteEnd},
				Width:    1,
			},
			level: level,
			kind:  kind,
			style: style,
		})
		currRune += numRunes
		lastNonTrans = i
		i = endIdx
	}

	return items
}
