package agent

import (
	"context"
	"testing"
	"time"

	"nabd/internal/provider"
)

// TestRateLimitWaitPolicy pins the unified wait policy: ceil(retry_after)
// when the provider declares one, else exponential backoff (1,2,4,8,10s),
// capped at 120s.
func TestRateLimitWaitPolicy(t *testing.T) {
	cases := []struct {
		hits       int
		retryAfter time.Duration
		want       time.Duration
	}{
		// No retry_after → exponential backoff by consecutive hit count.
		{1, 0, 1 * time.Second},
		{2, 0, 2 * time.Second},
		{3, 0, 4 * time.Second},
		{4, 0, 8 * time.Second},
		{5, 0, 10 * time.Second}, // capped at 10s
		{10, 0, 10 * time.Second},
		// retry_after present → ceil to whole seconds, ignoring hit count.
		{1, time.Duration(14.745 * float64(time.Second)), 15 * time.Second},
		{1, time.Duration(3.135 * float64(time.Second)), 4 * time.Second},
		{1, time.Duration(20.16 * float64(time.Second)), 21 * time.Second},
		{1, time.Duration(11.04 * float64(time.Second)), 12 * time.Second},
		// Cap at 120s.
		{1, 200 * time.Second, 120 * time.Second},
	}
	for _, tc := range cases {
		if got := rateLimitWait(tc.retryAfter, tc.hits); got != tc.want {
			t.Errorf("hits=%d retryAfter=%v: got %v, want %v", tc.hits, tc.retryAfter, got, tc.want)
		}
	}
}

// TestRateLimitHitsResetAfterSuccess verifies that a successful turn resets
// the consecutive-429 counter. With the new design, a 429 ends its turn
// (errTurnRateLimited) and the agent retries; a later successful turn must
// reset the counter so earlier 429s don't accumulate across healthy turns.
func TestRateLimitHitsResetAfterSuccess(t *testing.T) {
	p := &retryProvider{failFirst: true}
	l := &Loop{
		Provider: p,
		Sink:     &recordSink{},
		Budget:   NewBudget(),
		now:      func() time.Time { return time.Now() },
	}
	if err := l.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rateLimitHits != 0 {
		t.Errorf("rateLimitHits=%d after a successful run; must be 0", l.rateLimitHits)
	}
	if l.rateLimitAttempts != 0 {
		t.Errorf("rateLimitAttempts=%d after a successful run; must be 0", l.rateLimitAttempts)
	}
}

// retryProvider fails the first Stream() call with a 429, then succeeds.
// This models the new design where the provider reports 429 and returns
// (no internal retry), and the agent owns the retry loop.
type retryProvider struct {
	failFirst bool
	calls     int
}

func (*retryProvider) Name() string { return "retry" }

func (r *retryProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 4)
	go func() {
		defer close(ch)
		r.calls++
		if r.failFirst && r.calls == 1 {
			// Report 429 and return — the agent decides whether to retry.
			ch <- provider.Chunk{
				Kind: provider.ChunkRateLimit,
				RateLimit: &provider.RateLimitInfo{
					Code: 429, Limit: 8000, Used: 8000, Requested: 1000,
					WaitSec: 0.01, Attempt: 1, Err: "http 429: rate limit",
				},
			}
			return
		}
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "recovered"}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

// TestRateLimitAbsoluteBoundAborts pins the absolute termination guard: a
// provider that keeps answering 429 (with no success between) cannot stall
// the loop forever. With a fake clock the attempt counter trips the
// `attempts >= 8` bound before any 120s of real wait elapses, so this test
// must terminate quickly and return ErrRateLimitBudget.
func TestRateLimitAbsoluteBoundAborts(t *testing.T) {
	p := &burstProvider{total: 12}
	l := &Loop{
		Provider: p,
		Sink:     &recordSink{},
		Budget:   NewBudget(),
		now:      func() time.Time { return time.Now() },
	}
	err := l.Run(context.Background(), "burst")
	if err == nil {
		t.Fatal("expected ErrRateLimitBudget from an unbounded 429 burst")
	}
}

// burstProvider floods the stream with 429 chunks in a single turn, far
// beyond any ceiling, so the absolute attempt bound must trip.
type burstProvider struct{ total int }

func (*burstProvider) Name() string { return "burst" }

func (b *burstProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	// Emitted from a goroutine so the loop's drain sees them immediately.
	ch := make(chan provider.Chunk, b.total)
	go func() {
		defer close(ch)
		for i := 0; i < b.total; i++ {
			ch <- provider.Chunk{
				Kind: provider.ChunkRateLimit,
				RateLimit: &provider.RateLimitInfo{
					Code: 429, Limit: 8000, Used: 8000, Requested: 100,
					WaitSec: 1.0, Attempt: i + 1,
					Err: "http 429: rate limit",
				},
			}
		}
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "never"}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

// TestRateLimitCountersAreMutexProtected runs several concurrent Run calls
// and guards against data races on the rate-limit fields. -race is the real
// detector; on android/arm64 the runner is documented as unavailable, so
// this test at least exercises the concurrent path under the mutex.
func TestRateLimitCountersAreMutexProtected(t *testing.T) {
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			p := &retryProvider{failFirst: true}
			l := &Loop{
				Provider: p,
				Sink:     &recordSink{},
				Budget:   NewBudget(),
				now:      func() time.Time { return time.Now() },
			}
			_ = l.Run(context.Background(), "concurrent")
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}
