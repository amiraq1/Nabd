// engine.go — Nabd addition to the verbatim copy of golang.org/x/text
// v0.42.0 unicode/bidi (see ../provenance.md for the full record).
//
// It exposes the internal algorithm to nabd/internal/rtl with the corrected
// bracket pairing (BD16) required for Unicode conformance:
//
//   - closing brackets get reverseBracket(rune) as their pair identifier,
//     mirroring the package's own conformance test (core_test.go);
//   - bracket identifiers are canonicalised so the canonical-equivalent
//     bracket pairs (U+2329/U+232A versus U+3008/U+3009, per BidiBrackets.txt)
//     unify on both sides.
//
// Upstream v0.42.0 bidi.go:119 stores the raw rune for closing brackets, so
// its public Paragraph can never match an opener; that file is intentionally
// not part of this copy.

package bidi

import "fmt"

// Analysis is the result of running the algorithm over one paragraph.
type Analysis struct {
	// ParaLevel is the resolved paragraph embedding level (0 or 1).
	ParaLevel uint8
	// Levels holds one resolved embedding level per rune, after rule L1.
	Levels []uint8
	// Order is the rule L2 visual-to-logical permutation over all runes.
	Order []int
	// Classes holds the initial bidi class of every rune.
	Classes []Class
}

// Analyze runs the algorithm over text.
// base is -1 for auto (rules P2/P3), 0 for forced LTR, 1 for forced RTL.
func Analyze(text []rune, base int) (Analysis, error) {
	types := make([]Class, 0, len(text))
	pairTypes := make([]bracketType, 0, len(text))
	pairValues := make([]rune, 0, len(text))
	for _, r := range text {
		props, _ := LookupRune(r)
		types = append(types, props.Class())
		switch {
		case !props.IsBracket():
			pairTypes = append(pairTypes, bpNone)
			pairValues = append(pairValues, 0)
		case props.IsOpeningBracket():
			pairTypes = append(pairTypes, bpOpen)
			pairValues = append(pairValues, canonBracketRune(r))
		default:
			// Closing bracket: the pair identifier is the canonical form of
			// the mirrored opener, exactly as core_test.go builds it.
			pairTypes = append(pairTypes, bpClose)
			pairValues = append(pairValues, canonBracketRune(props.reverseBracket(r)))
		}
	}
	return run(types, pairTypes, pairValues, base)
}

// AnalyzeClasses runs the algorithm over abstract bidi-class sequences with no
// bracket information, as the BidiTest conformance file assumes.
func AnalyzeClasses(types []Class, base int) (Analysis, error) {
	pairTypes := make([]bracketType, len(types))
	pairValues := make([]rune, len(types))
	return run(types, pairTypes, pairValues, base)
}

func run(types []Class, pairTypes []bracketType, pairValues []rune, base int) (Analysis, error) {
	switch base {
	case -1, 0, 1:
	default:
		return Analysis{}, fmt.Errorf("bidi: invalid base level %d", base)
	}
	par, err := newParagraph(types, pairTypes, pairValues, level(base))
	if err != nil {
		return Analysis{}, err
	}
	rawLevels := par.getLevels([]int{len(types)})
	order := par.getReordering([]int{len(types)})
	a := Analysis{
		ParaLevel: uint8(par.embeddingLevel),
		Levels:    make([]uint8, len(rawLevels)),
		Order:     order,
		Classes:   types,
	}
	for i, l := range rawLevels {
		a.Levels[i] = uint8(l)
	}
	return a, nil
}

// canonBracketRune maps bracket runes to their canonical identifier for BD16.
// U+2329 LEFT-POINTING ANGLE BRACKET and U+232A RIGHT-POINTING ANGLE BRACKET
// have singleton canonical decompositions to U+3008/U+3009; BidiBrackets.txt
// pairs them cross-wise (2329↔3009, 3008↔232A), so canonicalising both
// identifiers unifies every official pair. This matches the NFKD step in
// core_test.go restricted to the only bracket runes that have decompositions.
func canonBracketRune(r rune) rune {
	switch r {
	case 0x2329:
		return 0x3008
	case 0x232A:
		return 0x3009
	default:
		return r
	}
}

// BracketKind reports the paired-bracket kind of r for rule BD16:
// 0 = not a bracket, 1 = opening, 2 = closing.
func BracketKind(r rune) uint8 {
	props, _ := LookupRune(r)
	switch {
	case props.IsOpeningBracket():
		return 1
	case props.IsBracket():
		return 2
	default:
		return 0
	}
}

// BracketPairID returns the pair identifier the engine assigns to r under
// BD16 (0 for non-brackets). Openers and the matching closers share the same
// identifier.
func BracketPairID(r rune) rune {
	props, _ := LookupRune(r)
	switch {
	case props.IsOpeningBracket():
		return canonBracketRune(r)
	case props.IsBracket():
		return canonBracketRune(props.reverseBracket(r))
	default:
		return 0
	}
}

// ReverseBracket returns the mirrored counterpart used by the paired-bracket
// algorithm (BD16 canonicalisation included).
func ReverseBracket(r rune) rune {
	return canonBracketRune(reverseBracketRaw(r))
}

func reverseBracketRaw(r rune) rune {
	props, _ := LookupRune(r)
	return props.reverseBracket(r)
}

// IsRemovedByX9 reports whether c is removed by rule X9.
func IsRemovedByX9(c Class) bool { return isRemovedByX9(c) }

// ClassName returns the UCD abbreviation of c.
func ClassName(c Class) string {
	if name, ok := className[c]; ok {
		return name
	}
	return "?"
}

var className = map[Class]string{
	L: "L", R: "R", AL: "AL", EN: "EN", ES: "ES", ET: "ET", AN: "AN",
	CS: "CS", NSM: "NSM", BN: "BN", B: "B", S: "S", WS: "WS", ON: "ON",
	LRE: "LRE", RLE: "RLE", PDF: "PDF", LRO: "LRO", RLO: "RLO",
	LRI: "LRI", RLI: "RLI", FSI: "FSI", PDI: "PDI",
}

// ClassFromName parses a UCD bidi-class abbreviation (BidiTest.txt tokens).
func ClassFromName(s string) (Class, bool) {
	for c, name := range className {
		if name == s {
			return c, true
		}
	}
	return 0, false
}
