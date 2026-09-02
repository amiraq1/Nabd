package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nabd/internal/provider"
)

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
			ch <- provider.Chunk{
				Kind: provider.ChunkRateLimit,
				RateLimit: &provider.RateLimitInfo{
					Code:      429,
					Limit:     8000,
					Used:      1859,
					Requested: 6778,
					WaitSec:   4.7775,
					Attempt:   1,
					Err:       "http 429: Rate limit reached for model qwen/qwen3.8-27b",
				},
			}
			ch <- provider.Chunk{
				Kind: provider.ChunkText,
				Text: "recovered response",
			}
			ch <- provider.Chunk{
				Kind: provider.ChunkStop,
				Stop: "end_turn",
			}
		}
	}()
	return ch, nil
}

type recordSink struct {
	evs []Event
}

func (r *recordSink) Emit(e Event) error {
	r.evs = append(r.evs, e)
	return nil
}

func TestLoopEmitsRateLimitEventToJournal(t *testing.T) {
	sink := &recordSink{}
	loop := &Loop{
		Provider: &mockRateLimitProvider{},
		Sink:     sink,
		System:   "test",
		Budget:   NewBudget(),
	}

	_, _, err := loop.streamTurn(context.Background(), []provider.Message{{Role: provider.User, Text: "hi"}})
	if err != nil {
		t.Fatalf("streamTurn failed: %v", err)
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
		`"attempt":1`,
		`"err":"http 429: Rate limit reached for model qwen/qwen3.8-27b"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("serialized EventRateLimit missing field %s\ncontent:\n%s", want, content)
		}
	}
}
