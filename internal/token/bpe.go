package token

import (
	"strings"
	"unicode"
)

// Merge is one BPE merge rule: the pair (a, b) collapses to new_rank.
// Ranks 0–255 are the byte values; ranks ≥256 are produced by merges,
// in order. Storing three integers per merge (not a 100k-entry string
// map) keeps the embedded table small — a phone-first project cannot
// ship a 40MB binary for 15% accuracy (see docs/TECH_DEBT.md, NBD-xxx).
type Merge struct {
	A, B    uint16
	NewRank uint16
}

// pairKey packs two uint16 ranks into one uint32 map key.
func pairKey(a, b uint16) uint32 { return uint32(a)<<16 | uint32(b) }

// BPETokenizer encodes text with the standard tiktoken merge loop:
//  1. pre-tokenize (split on a boundary rule),
//  2. start each pre-token as a sequence of byte ranks (0–255),
//  3. repeatedly collapse the lowest-ranked adjacent pair that has a
//     merge rule, until none do.
//
// The token count is the length of the final rank sequence.
//
// The split rule here is intentionally simple (whitespace + punctuation
// + case boundary) and exists only so the merge loop has units to work
// on. The encoder tables for a real model (cl100k_base, ~100k merges)
// carry the actual lexical knowledge; the algorithm is the same.
type BPETokenizer struct {
	// merge maps packed pair (a<<16 | b) → new_rank. A missing key means
	// no merge for that pair. Sparse: memory tracks the real table size,
	// not the rank range (which reaches ~100k for cl100k_base).
	merge map[uint32]uint16
}

// NewBPETokenizer builds a tokenizer from a merge table. Each merge's
// NewRank must be ≥ 256 (0–255 are reserved for raw bytes).
func NewBPETokenizer(merges []Merge) *BPETokenizer {
	t := &BPETokenizer{merge: make(map[uint32]uint16, len(merges))}
	for _, m := range merges {
		t.merge[pairKey(m.A, m.B)] = m.NewRank
	}
	return t
}

// Count returns the number of tokens text encodes to.
func (t *BPETokenizer) Count(text string) int {
	if t == nil {
		return HeuristicTokenizer{}.Count(text)
	}
	total := 0
	for _, unit := range split(text) {
		// Start: one rank per byte.
		ranks := make([]uint16, 0, len(unit))
		for i := 0; i < len(unit); i++ {
			ranks = append(ranks, uint16(unit[i]))
		}
		// Merge loop: find the lowest-ranked mergeable pair, collapse it,
		// repeat. Each pass scans O(n); the number of passes is bounded by
		// the merge depth (log V for a vocab of size V), so the whole
		// encoding is O(n log V) — linearish for short pre-tokens.
		for {
			bestIdx := -1
			bestRank := uint16(0xffff)
			for i := 0; i < len(ranks)-1; i++ {
				if m, ok := t.merge[pairKey(ranks[i], ranks[i+1])]; ok && m < bestRank {
					bestRank = m
					bestIdx = i
				}
			}
			if bestIdx < 0 {
				break
			}
			ranks[bestIdx] = bestRank
			ranks = append(ranks[:bestIdx+1], ranks[bestIdx+2:]...)
		}
		total += len(ranks)
	}
	return total
}

// split breaks text into pre-tokenization units. The rule mirrors the
// shape of the cl100k_base contract (whitespace runs separate; each
// contiguous run of letters/digits is one unit) without importing the
// full regex — this is a test encoder, the algorithm is what matters.
func split(text string) []string {
	var units []string
	var sb strings.Builder
	flush := func() {
		if sb.Len() > 0 {
			units = append(units, sb.String())
			sb.Reset()
		}
	}
	for _, r := range text {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			flush()
			units = append(units, string(r))
		default:
			sb.WriteRune(r)
		}
	}
	flush()
	return units
}

var _ Tokenizer = (*BPETokenizer)(nil)
