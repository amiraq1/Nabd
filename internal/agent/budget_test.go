package agent

import (
	"math"
	"testing"

	"nabd/internal/provider"
)

// TestCalibrateRatchetRisesOnly: within a session the calibration ratio may
// only rise ("take the worst"). A smoothed blend (ratio*0.7 + obs*0.3) drags
// a strong upward reading down to 30% of headroom — a 1.80 observation
// against a 1.0 base lands at 1.24 instead of 1.80, which is exactly the
// 1.50->1.42 drift path that kills a session on the next long Arabic file.
// Calibrate must therefore adopt the observation whole on a rise and pin it
// on a fall; EMA has no safe direction here.
func TestCalibrateRatchetRisesOnly(t *testing.T) {
	// NewBudget starts every session at ratio 1.0.
	b := NewBudget()

	// Downward reading first: obs=0.8 < base 1.0 -> pinned, no move.
	if b.Calibrate(80, 100) {
		t.Fatal("downward observation must not lower the ratio")
	}
	if math.Abs(b.Ratio()-1) > 1e-9 {
		t.Fatalf("ratio=%v, want 1.0 (pinned)", b.Ratio())
	}

	// Genuine upward reading: adopt the FULL observation (1.8), not 1.24.
	if !b.Calibrate(180, 100) {
		t.Fatal("upward observation rejected")
	}
	if math.Abs(b.Ratio()-1.8) > 1e-9 {
		t.Fatalf("ratio=%v, want 1.8 (full jump, not EMA 1.24)", b.Ratio())
	}

	// After the rise, a lower reading is pinned to the high-water mark.
	if b.Calibrate(90, 100) {
		t.Fatal("ratio fell after the ratchet")
	}
	if math.Abs(b.Ratio()-1.8) > 1e-9 {
		t.Fatalf("ratio=%v, want pinned 1.8", b.Ratio())
	}

	// Credible-but-above-ceiling (->3.0) clamps to maxRatio (2.0) and moves.
	if !b.Calibrate(300, 100) {
		t.Fatal("ceiling-clamped rise should still move the ratio")
	}
	if math.Abs(b.Ratio()-2.0) > 1e-9 {
		t.Fatalf("ratio=%v, want clamped 2.0", b.Ratio())
	}
	// At the ceiling: a higher reading is a no-op, never above maxRatio.
	if b.Calibrate(400, 100) {
		t.Fatal("ratio must not rise past the ceiling")
	}
	if math.Abs(b.Ratio()-2.0) > 1e-9 {
		t.Fatalf("ratio=%v, want still 2.0", b.Ratio())
	}
}

// TestCalibrateGuards: degenerate inputs cannot corrupt the ratio or pin Inf.
func TestCalibrateGuards(t *testing.T) {
	b := NewBudget()
	for _, in := range [][2]int{{0, 100}, {100, 0}, {0, 0}, {-1, 100}, {100, -1}} {
		if b.Calibrate(in[0], in[1]) {
			t.Fatalf("Calibrate(%d,%d) must be a no-op", in[0], in[1])
		}
	}
	if math.Abs(b.Ratio()-1) > 1e-9 {
		t.Fatalf("ratio=%v, want 1.0 after degenerate inputs", b.Ratio())
	}

	// Equality: obs already equals the ratio -> no spurious journal entry.
	if b.Calibrate(100, 100) {
		t.Fatal("obs==ratio must be a no-op")
	}
	if math.Abs(b.Ratio()-1) > 1e-9 {
		t.Fatalf("ratio=%v, want 1.0", b.Ratio())
	}
}

// TestCalibrateZeroBudgetFirstRise: a zero-value Budget (ratio 0) still jumps
// to the full observation on its first measurement rather than lingering at
// the EMA 30% blend — there is no session history to smooth against.
func TestCalibrateZeroBudgetFirstRise(t *testing.T) {
	b := &Budget{}
	if !b.Calibrate(180, 100) {
		t.Fatal("first rise must adopt the observation")
	}
	if math.Abs(b.Ratio()-1.8) > 1e-9 {
		t.Fatalf("ratio=%v, want 1.8 (full jump from zero base)", b.Ratio())
	}
}

// TestCalibrateTracksError: Calibrate records the estimation error so /ctx
// can show the human whether the estimate is trustworthy. After
// Calibrate(500, 400) the estimate was 20% low, so LastError == 0.2
// (|500 − 400| / 500).
func TestCalibrateTracksError(t *testing.T) {
	b := NewBudget()
	if !b.Calibrate(500, 400) {
		t.Fatal("calibration should move the ratio (1.25 > 1.0)")
	}
	if math.Abs(b.LastError()-0.2) > 1e-9 {
		t.Fatalf("LastError=%v, want 0.2", b.LastError())
	}
	if math.Abs(b.WorstError()-0.2) > 1e-9 {
		t.Fatalf("WorstError=%v, want 0.2", b.WorstError())
	}
	// A second calibration with a larger error updates both.
	if !b.Calibrate(600, 400) {
		t.Fatal("calibration should move the ratio (1.5 > 1.25)")
	}
	if math.Abs(b.LastError()-0.333333) > 1e-4 {
		t.Fatalf("LastError=%v, want ~0.333", b.LastError())
	}
	if math.Abs(b.WorstError()-0.333333) > 1e-4 {
		t.Fatalf("WorstError=%v, want ~0.333 (max)", b.WorstError())
	}
	// Calibrated() is true once any calibration has happened.
	if !b.Calibrated() {
		t.Fatal("Calibrated() must be true after a calibration")
	}
}

// TestCalibrateErrorNoCalibration: before any calibration, LastError and
// WorstError are 0 and Calibrated() is false — /ctx shows "uncalibrated".
func TestCalibrateErrorNoCalibration(t *testing.T) {
	b := NewBudget()
	if b.LastError() != 0 || b.WorstError() != 0 {
		t.Fatalf("before calibration: LastError=%v WorstError=%v, want 0 0", b.LastError(), b.WorstError())
	}
	if b.Calibrated() {
		t.Fatal("Calibrated() must be false before any calibration")
	}
}

// TestBudgetEstimateUsesTokenizer: when a tokenizer is installed, Estimate
// uses it for the text portions instead of the chars/4 heuristic. For
// Arabic text the two diverge, so the counts must differ. This is the
// acceptance assertion that the estimator is never used when a real
// tokenizer is available.
func TestBudgetEstimateUsesTokenizer(t *testing.T) {
	b := NewBudget()
	ms := []provider.Message{{Role: provider.User, Text: "مرحبا بالعالم هذا نص طويل للاختبار"}}

	heuristic := b.Estimate(ms)

	// Install a tokenizer that returns a fixed small count per call.
	fixed := &fakeTokenizer{n: 3}
	b.SetTokenizer(fixed)

	withTokenizer := b.Estimate(ms)

	// The tokenizer path counts 3 tokens per message text; the heuristic
	// counts runes/1.6 (much larger for Arabic). They must differ.
	if withTokenizer == heuristic {
		t.Fatalf("tokenizer path must differ from heuristic: both=%d", withTokenizer)
	}
	if withTokenizer >= heuristic {
		t.Fatalf("tokenizer count (%d) should be < heuristic count (%d) for this Arabic text", withTokenizer, heuristic)
	}

	// Removing the tokenizer reverts to the heuristic.
	b.SetTokenizer(nil)
	if got := b.Estimate(ms); got != heuristic {
		t.Fatalf("after removing tokenizer: Estimate=%d, want %d (heuristic)", got, heuristic)
	}
}

// fakeTokenizer returns a fixed Count regardless of input, for tests.
type fakeTokenizer struct{ n int }

func (f *fakeTokenizer) Count(text string) int { return f.n }
