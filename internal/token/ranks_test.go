package token

// testRanks is a tiny, hand-verifiable merge table for golden-vector
// tests. It is NOT a real tokenizer — it exists only so the merge loop
// has deterministic input to prove itself against.
//
// The merge loop always collapses the LOWEST-RANKED adjacent pair first,
// so the rank numbers encode merge priority. Ranks 0–255 are raw bytes:
//
//	256 = 't' 'h'   (bytes 116, 104) — merges first where present
//	257 = 256 'e'   ("the" → one token, via two passes)
//	258 = 'w' 'o'   (bytes 119, 111)
//	259 = 258 'r'   ("wor" → one token)
//	260 = 'h' 'e'   (bytes 104, 101) — higher rank than 256, so "the"
//	                  merges as 256→257, never as 260
//
// Verified encodings (see TestGoldenVectors):
//
//	"the"      → [257]            = 1 token
//	"he"       → [260]            = 1 token
//	"they"     → [257, 121]       = 2 tokens  ('t''h'→256, +256'e'→257, +y)
//	"world"    → [259, 108, 100]  = 3 tokens  ('w''o'→258, +258'r'→259, +l+d)
//	"the world"→ 1 + 3            = 4 tokens  (space splits units)
var testRanks = []Merge{
	{A: 't', B: 'h', NewRank: 256},
	{A: 256, B: 'e', NewRank: 257}, // "the"
	{A: 'w', B: 'o', NewRank: 258},
	{A: 258, B: 'r', NewRank: 259}, // "wor"
	{A: 'h', B: 'e', NewRank: 260}, // "he"
}

// testTok is the shared tokenizer for golden-vector tests.
var testTok = NewBPETokenizer(testRanks)
