// Package agent: budget.go guesses token counts without shipping a tokenizer.
// It errs high on purpose: an overestimate compacts a turn too early, an
// underestimate hits a hard API rejection mid-sentence. Arabic is the reason
// the naive chars/4 rule fails — non-ASCII runes cost far more per rune.
package agent

import (
	"math"
	"strconv"
	"sync"
	"unicode"

	"nabd/internal/config"
	"nabd/internal/provider"
	"nabd/internal/token"
)

const (
	runesPerTokASCII = 4.0
	runesPerTokOther = 1.6 // Arabic sits near 1.8; 1.6 leans safe
	perMessage       = 8   // role framing, delimiters
	perToolCall      = 20
)

func EstimateText(s string) int {
	var a, o int
	for _, r := range s {
		if r < unicode.MaxASCII {
			a++
			continue
		}
		o++
	}
	return int(float64(a)/runesPerTokASCII + float64(o)/runesPerTokOther)
}

func EstimateMessages(ms []provider.Message) int {
	n := 0
	for _, m := range ms {
		n += perMessage + EstimateText(m.Text)
		for _, c := range m.ToolCalls {
			n += perToolCall + EstimateText(c.Name) + EstimateText(string(c.Input))
		}
		for _, r := range m.ToolResults {
			n += perMessage + EstimateText(r.Output)
		}
	}
	return n
}

// Budget knows the window and corrects itself. Providers report real input
// counts; one honest number beats any heuristic, so the ratio is learned.
type Budget struct {
	mu      sync.Mutex
	Limit   int // context window
	Reserve int // room for the reply plus the system prompt
	ratio   float64
	// tokenizer, when set, replaces EstimateText for message estimation.
	// nil means "use the heuristic" — the safe default for an unknown model.
	tokenizer token.Tokenizer
	// lastError is |actual − estimated| / actual of the most recent
	// calibration point; worstError is the session max. Both are 0 until
	// the first calibration.
	lastError, worstError float64
}

func init() {
	// Register the canonical chars/4 estimator into the token package so
	// HeuristicTokenizer uses the same implementation as EstimateText.
	// (The token package cannot import agent — that would cycle — so the
	// registration flows the other way.)
	token.SetTextHeuristic(EstimateText)
}

func NewBudget() *Budget {
	b := &Budget{Limit: 120000, Reserve: 16000, ratio: 1}
	if v := config.Get("NABD_CTX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 8000 {
			b.Limit = n
		}
	}
	return b
}

// SetTokenizer installs a real tokenizer for message estimation. Nil
// reverts to the heuristic. Called once at startup from the active
// provider's name; safe to call later if the model changes mid-session.
func (b *Budget) SetTokenizer(t token.Tokenizer) {
	b.mu.Lock()
	b.tokenizer = t
	b.mu.Unlock()
}

// maxOutputTokens is the output reservation (max_tokens) sent to the
// provider. Experimental value: the implicit 4096 ate half the TPM budget
// on output, starving input. NABD_MAX_TOKENS overrides; values outside
// [minMaxTokens, maxMaxTokens] or non-numeric fall back to the default.
const (
	defaultMaxTokens = 1024
	minMaxTokens     = 128
	maxMaxTokens     = 8192
)

func maxOutputTokens() int {
	if v := config.Get("NABD_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= minMaxTokens && n <= maxMaxTokens {
			return n
		}
	}
	return defaultMaxTokens
}

func (b *Budget) Usable() int { return b.Limit - b.Reserve }

func (b *Budget) Estimate(ms []provider.Message) int {
	b.mu.Lock()
	r := b.ratio
	tok := b.tokenizer
	b.mu.Unlock()
	if tok != nil {
		return int(float64(estimateMessagesWith(ms, tok)) * r)
	}
	return int(float64(EstimateMessages(ms)) * r)
}

// estimateMessagesWith is EstimateMessages using a real tokenizer instead
// of the chars/4 heuristic for the text portions. The per-message and
// per-tool-call framing overheads stay the same — only the text count
// changes.
func estimateMessagesWith(ms []provider.Message, tok token.Tokenizer) int {
	n := 0
	for _, m := range ms {
		n += perMessage + tok.Count(m.Text)
		for _, c := range m.ToolCalls {
			n += perToolCall + tok.Count(c.Name) + tok.Count(string(c.Input))
		}
		for _, r := range m.ToolResults {
			n += perMessage + tok.Count(r.Output)
		}
	}
	return n
}

// Ratio exposes the current calibration factor, for journaling.
func (b *Budget) Ratio() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ratio
}

// Calibrate folds a real input_tokens count back into the ratio. Two rules
// make it conservative by construction:
//
//  1. Take the worst seen this session, not a smoothed average. A blend
//     (ratio*0.7 + obs*0.3) drags a strong upward reading down to 30% of
//     headroom: a 1.80 observation against a 1.0 base lands at 1.24 — still
//     31% under the truth that tripped a mid-sentence 413. A downward blend
//     leaks worst-case upward the other way. There is no safe smoothing
//     direction, so adopt the observation whole on a rise and pin on a fall.
//  2. The ratchet: within a session the ratio may only rise. A low reading
//     (Latin text underestimates) is ignored, so the next long Arabic file
//     still meets the worst-case bound that was already in force.
//
// Returns true when the ratio moved, so the caller can journal the adopted
// value. Unit: ratio = observed prompt_tokens ÷ heuristic estimate (a
// dimensionless correction factor on the chars/4 heuristic — NOT bytes per
// token; the 3.56/4.00 figures in NOTES are the latter, a different axis).
func (b *Budget) Calibrate(actual, estimated int) bool {
	if actual <= 0 || estimated <= 0 {
		return false // a provider that reports no usage must not corrupt the ratio
	}
	obs := float64(actual) / float64(estimated)
	if math.IsNaN(obs) || math.IsInf(obs, 0) {
		return false // defensive: never pin +Inf on the high-water mark
	}
	// A wild single measurement is clamped before it can move the ratio far:
	// observations outside [minObsRatio, maxObsRatio] are ignored entirely.
	if obs < minObsRatio || obs > maxObsRatio {
		return false
	}
	// Take the worst, not the average: a blend on the rise only invites a
	// mid-sentence 413 later in the same session.
	b.mu.Lock()
	defer b.mu.Unlock()
	next := obs
	if next < minRatio {
		next = minRatio
	}
	if next > maxRatio {
		next = maxRatio
	}
	// Track how far off the estimate was at this calibration point, so
	// /ctx can show the human whether the estimate is trustworthy. The
	// error is |actual − estimated| / actual — a dimensionless fraction.
	// Done for every valid observation, even when the ratio does not move.
	if estimated > 0 {
		err := math.Abs(float64(actual-estimated)) / float64(actual)
		if !(math.IsNaN(err) || math.IsInf(err, 0)) {
			b.lastError = err
			if err > b.worstError {
				b.worstError = err
			}
		}
	}
	// Conservative ratchet: downward drift is the observed failure mode
	// (1.50 -> 1.42); pin to the session high-water mark instead of
	// accepting the lower reading.
	if next < b.ratio {
		next = b.ratio
	}
	if next == b.ratio {
		return false
	}
	b.ratio = next
	return true
}

// LastError returns |actual − estimated| / actual of the most recent
// calibration point. 0 until the first calibration (or when the only
// observations were degenerate). A value of 0.2 means the estimate was
// 20% off.
func (b *Budget) LastError() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastError
}

// WorstError returns the max LastError seen this session. 0 until the
// first calibration.
func (b *Budget) WorstError() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.worstError
}

// Calibrated reports whether any calibration has happened this session
// (i.e. LastError is meaningful). Exposed so /ctx can show
// "uncalibrated" instead of a misleading "0%".
func (b *Budget) Calibrated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.worstError > 0
}

const (
	minObsRatio = 0.5 // below this the measurement is not credible (e.g. a 0)
	maxObsRatio = 4.0 // above this the measurement is not credible
	minRatio    = 0.6 // floor: the heuristic must never be trusted below 0.6x reality
	maxRatio    = 2.0 // ceiling: one credible-but-sparse reading cannot hoist the cap; clamped values are logged with a notice
)

func (b *Budget) Pressure(ms []provider.Message) float64 {
	return float64(b.Estimate(ms)) / float64(b.Usable())
}
