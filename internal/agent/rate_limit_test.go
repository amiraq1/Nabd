package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nabd/internal/provider"
)

// recordSink captures emitted events for assertions.
type recordSink struct {
	evs []Event
}

func (r *recordSink) Emit(e Event) error {
	r.evs = append(r.evs, e)
	return nil
}

// mockRateLimitProvider returns a single 429 on the first call, then a
// healthy answer on the second call. This models the new design where the
// provider reports 429 and returns (no internal retry); the agent owns the
// retry loop.
type mockRateLimitProvider struct {
	attempts int
}

func (m *mockRateLimitProvider) Name() string { return "mock-rate-limit" }

func (m *mockRateLimitProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 10)
	go func() {
		defer close(ch)
		m.attempts++
		if m.attempts == 1 {
			// Report 429 and return — the agent decides whether to retry.
			ch <- provider.Chunk{
				Kind: provider.ChunkRateLimit,
				RateLimit: &provider.RateLimitInfo{
					Code:          429,
					Limit:         8000,
					Used:          1859,
					Requested:     6778,
					WaitSec:       4.7775,
					Attempt:       1,
					Err:           "http 429: Rate limit reached for model qwen/qwen3.8-27b",
					RetryAfter:    4.7775,
					RawMessage:    `{"error":{"message":"Rate limit reached for model qwen/qwen3.8-27b\nLimit 8000, Used 1859, Requested 6778. Please try again in 4.7775s."}}`,
					RawRetryAfter: "Please try again in 4.7775s.",
				},
			}
			return
		}
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "recovered response"}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

// TestLoopEmitsRateLimitEventToJournal verifies that a 429 reported by the
// provider is serialized to the journal with all six raw fields, including
// the new RawRetryAfter field.
func TestLoopEmitsRateLimitEventToJournal(t *testing.T) {
	sink := &recordSink{}
	loop := &Loop{
		Provider: &mockRateLimitProvider{},
		Sink:     sink,
		System:   "test",
		Budget:   NewBudget(),
	}

	// The first turn ends in a 429 (streamTurn returns errTurnRateLimited).
	_, _, err := loop.streamTurn(context.Background(), []provider.Message{{Role: provider.User, Text: "hi"}})
	if err == nil {
		t.Fatal("expected errTurnRateLimited from a 429 turn")
	}

	var rlEvent *Event
	for _, e := range sink.evs {
		if e.Type == EventRateLimit {
			rlEvent = &e
			break
		}
	}
	if rlEvent == nil {
		t.Fatalf("expected EventRateLimit to be emitted, got %d events", len(sink.evs))
	}
	for _, e := range sink.evs {
		if e.Type == EventRateLimit {
			rlEvent = &e
			break
		}
	}
	if rlEvent == nil {
		t.Fatalf("expected EventRateLimit to be emitted, got %d events", len(sink.evs))
	}

	data, err := json.Marshal(rlEvent)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		`"type":"rate_limit"`,
		`"code":429`,
		`"limit":8000`,
		`"used":1859`,
		`"requested":6778`,
		`"wait_s":4.7775`,
		`"retry_after":4.7775`,
		`"raw_message":"{\"error\":{\"message\":\"Rate limit reached`,
		`"attempt":1`,
		`"err":"http 429: Rate limit reached for model qwen/qwen3.8-27b"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("serialized EventRateLimit missing field %s\ncontent:\n%s", want, content)
		}
	}
}

type breakerProvider struct {
	emitted int
}

func (b *breakerProvider) Name() string { return "breaker" }

func (b *breakerProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 10)
	go func() {
		defer close(ch)
		for i := 0; i < 3; i++ {
			b.emitted++
			ch <- provider.Chunk{
				Kind: provider.ChunkRateLimit,
				RateLimit: &provider.RateLimitInfo{
					Code:      429,
					Limit:     8000,
					Used:      8000,
					Requested: 1000,
					WaitSec:   1.0,
					Attempt:   b.emitted,
					Err:       "http 429: rate limit",
				},
			}
		}
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "never reached"}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

func TestCircuitBreaker(t *testing.T) {
	sink := &recordSink{}
	p := &breakerProvider{}
	l := &Loop{
		Provider:        p,
		Sink:            sink,
		System:          "test",
		Budget:          NewBudget(),
		RateLimitBudget: 3,
	}

	err := l.Run(context.Background(), "hi")
	if !errors.Is(err, ErrRateLimitBudget) {
		t.Fatalf("expected ErrRateLimitBudget, got: %v", err)
	}

	var hasNotice, hasRunError bool
	for _, e := range sink.evs {
		if e.Type == Notice && strings.Contains(e.Text, "rate limit budget exhausted") {
			hasNotice = true
		}
		if e.Type == RunError && e.Err == ErrRateLimitBudget.Error() {
			hasRunError = true
		}
	}
	if !hasNotice {
		t.Errorf("expected notice with 'rate limit budget exhausted'")
	}
	if !hasRunError {
		t.Errorf("expected RunError with ErrRateLimitBudget")
	}
}
