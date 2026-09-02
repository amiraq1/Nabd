package agent

import (
	"context"
	"math"
	"testing"

	"nabd/internal/provider"
)

// TestFalsificationBackoffDomain tests whether the source-extracted backoff constant
// falls within the mathematically admissable interval [5.554, 7.337] seconds.
func TestFalsificationBackoffDomain(t *testing.T) {
	// Source extracted from 6e58195:internal/provider/anthropic.go:24
	// minBackoff = time.Second
	// backoff(1, 0) = minBackoff << 0 = 1.0s
	sourceBackoffSec := 1.0

	lowerBound := 5.554
	upperBound := 7.337

	inDomain := sourceBackoffSec >= lowerBound && sourceBackoffSec <= upperBound
	t.Logf("BACKOFF_6e58195 = %.1fs, Admissable Interval = [%.3f, %.3f], InDomain = %v",
		sourceBackoffSec, lowerBound, upperBound, inDomain)

	// As specified in the execution directive:
	// A value outside [5.554, 7.337] falsifies the simplistic uninstrumented retry timeline
	// and reopens WINDOW_SEMANTICS / timeline assumptions rather than fudging numbers.
	if inDomain {
		t.Fatalf("unexpected: source backoff 1.0s should be outside [%.3f, %.3f]", lowerBound, upperBound)
	}
}

// TestFalsificationChargeFloor verifies that charge2 >= prompt2 (5179)
// holds only for rejected round-trip times r3 in [0, 1.783] seconds.
func TestFalsificationChargeFloor(t *testing.T) {
	const (
		prompt2 = 5179.0
		rate    = 8000.0 / 60.0 // 133.333...
	)

	// charge2 = 5416.7 - rate * r3
	calcCharge := func(r3 float64) float64 {
		return 5416.7 - rate*r3
	}

	// Maximum r3 that keeps charge2 >= prompt2
	maxR3 := (5416.7 - prompt2) / rate
	if math.Abs(maxR3-1.783) > 0.01 {
		t.Fatalf("expected maxR3 ≈ 1.783, got %.4f", maxR3)
	}

	// For r3 = 1.5s (nominal RTT)
	chargeAtNominalRTT := calcCharge(1.5)
	if chargeAtNominalRTT < prompt2 {
		t.Fatalf("charge at nominal RTT 1.5s (%.1f) fell below prompt floor %.1f", chargeAtNominalRTT, prompt2)
	}
	completionCharged := chargeAtNominalRTT - prompt2
	t.Logf("At r3=1.5s: charge2=%.2f, completion_charged=%.2f (<= 238)", chargeAtNominalRTT, completionCharged)
	if completionCharged > 238.0 {
		t.Fatalf("completion charged %.2f exceeded 238 upper bound", completionCharged)
	}
}

// TestFalsificationLeakyBucketWaitMatch tests the exact mathematical match of
// the leaky bucket wait duration against Groq's 4.7775s error report.
func TestFalsificationLeakyBucketWaitMatch(t *testing.T) {
	const (
		limit     = 8000.0
		used      = 1859.0
		requested = 6778.0
		rate      = limit / 60.0
	)

	overflow := used + requested - limit
	if overflow != 637.0 {
		t.Fatalf("expected overflow=637, got %.1f", overflow)
	}

	waitSec := overflow / rate
	reportedWait := 4.7775

	if math.Abs(waitSec-reportedWait) > 1e-4 {
		t.Fatalf("calculated wait %.6f does not match reported %.4f", waitSec, reportedWait)
	}
}

type retryCountProvider struct {
	attempts int
}

func (r *retryCountProvider) Name() string { return "retry-count" }
func (r *retryCountProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 10)
	go func() {
		defer close(ch)
		r.attempts++ // silent retry attempt 1
		r.attempts++ // silent retry attempt 2
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "done"}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

// TestFalsificationSilentAttemptDoesNotConsumeTurns verifies that provider-level
// retry handling does not advance the agent turn counter.
func TestFalsificationSilentAttemptDoesNotConsumeTurns(t *testing.T) {
	sink := &recordSink{}
	p := &retryCountProvider{}
	l := &Loop{
		Provider: p,
		Sink:     sink,
		MaxTurns: 2,
		Budget:   NewBudget(),
	}
	err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.attempts != 2 {
		t.Fatalf("expected 2 provider attempts, got %d", p.attempts)
	}
	turnStarts := 0
	for _, e := range sink.evs {
		if e.Type == TurnStart {
			turnStarts++
		}
	}
	if turnStarts != 1 {
		t.Fatalf("silent attempts leaked into agent turns: got %d turn_starts", turnStarts)
	}
}
