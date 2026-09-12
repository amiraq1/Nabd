package agent

import (
	"math"
	"testing"
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
