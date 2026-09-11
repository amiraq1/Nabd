package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nabd/internal/provider"
)

type readyBlockingProvider struct {
	readyCh chan<- struct{}
	unblock <-chan struct{}
}

func (b *readyBlockingProvider) Name() string { return "ready-blocking" }

func (b *readyBlockingProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	go func() {
		defer close(ch)
		b.readyCh <- struct{}{}
		<-b.unblock
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "summary text"}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

type countingProvider struct{ calls int }

func (c *countingProvider) Name() string { return "counting" }

func (c *countingProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	c.calls++
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	close(ch)
	return ch, nil
}

func TestCompactBoundaryValidationRace(t *testing.T) {
	t.Run("rejects_orphaned_tool_end", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		unblock := make(chan struct{})
		l := &Loop{Provider: &readyBlockingProvider{readyCh: ready, unblock: unblock}, Budget: NewBudget()}
		orphanID := "orphan-call-1"
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})
		l.emit(Event{Type: TurnStart})
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: orphanID, Name: "read_file"}})
		l.emit(Event{Type: UserMsg, Text: "short user turn two"})

		errCh := make(chan error, 1)
		go func() { errCh <- l.Compact(context.Background(), 50) }()
		<-ready
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: orphanID, OK: true, Output: "done"}})
		l.emit(Event{Type: TurnEnd})
		close(unblock)

		if err := <-errCh; !errors.Is(err, ErrCompactBoundaryStale) {
			t.Fatalf("Compact error = %v, want ErrCompactBoundaryStale", err)
		}
		for _, e := range l.Hist() {
			if e.Type == Compact {
				t.Fatal("Compact event appended after stale-boundary rejection")
			}
		}
	})

	t.Run("accepts_safe_concurrent_append", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		unblock := make(chan struct{})
		l := &Loop{Provider: &readyBlockingProvider{readyCh: ready, unblock: unblock}, Budget: NewBudget()}
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})
		l.emit(Event{Type: UserMsg, Text: "short user turn two"})

		errCh := make(chan error, 1)
		go func() { errCh <- l.Compact(context.Background(), 50) }()
		<-ready
		l.emit(Event{Type: UserMsg, Text: "safe concurrent turn"})
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: "safe-call", Name: "read_file"}})
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: "safe-call", OK: true, Output: "done"}})
		l.emit(Event{Type: TurnEnd})
		close(unblock)

		if err := <-errCh; err != nil {
			t.Fatalf("safe concurrent append rejected: %v", err)
		}
		if !rawPairingInvariantHolds(Live(l.Hist())) {
			t.Fatal("successful compact produced invalid raw pairing")
		}
	})

	t.Run("accepts_malformed_tool_end", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		unblock := make(chan struct{})
		close(unblock)
		l := &Loop{Provider: &readyBlockingProvider{readyCh: ready, unblock: unblock}, Budget: NewBudget()}
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})
		l.emit(Event{Type: UserMsg, Text: "legacy turn"})
		l.emit(Event{Type: ToolEnd})
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{Name: "read_file"}})
		if err := l.Compact(context.Background(), 50); err != nil {
			t.Fatalf("malformed legacy ToolEnd blocked compact: %v", err)
		}
	})

	t.Run("pre_validation_avoids_provider", func(t *testing.T) {
		prov := &countingProvider{}
		l := &Loop{Provider: prov, Budget: NewBudget()}
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: "orphan", Name: "read_file"}})
		l.emit(Event{Type: UserMsg, Text: "short user turn"})
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: "orphan", OK: true}})
		if err := l.Compact(context.Background(), 50); !errors.Is(err, ErrCompactBoundaryStale) {
			t.Fatalf("Compact error = %v, want ErrCompactBoundaryStale", err)
		}
		if prov.calls != 0 {
			t.Fatalf("unsafe snapshot made %d provider calls", prov.calls)
		}
	})

	t.Run("rejects_concurrent_rewind_before_boundary_stales", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		unblock := make(chan struct{})
		l := &Loop{Provider: &readyBlockingProvider{readyCh: ready, unblock: unblock}, Budget: NewBudget()}
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})
		l.emit(Event{Type: UserMsg, Text: "short user turn"})

		errCh := make(chan error, 1)
		go func() { errCh <- l.Compact(context.Background(), 50) }()
		<-ready
		if _, err := l.Rewind(1); !errors.Is(err, ErrHistoryMutationInProgress) {
			t.Fatalf("Rewind error = %v, want ErrHistoryMutationInProgress", err)
		}
		close(unblock)
		if err := <-errCh; err != nil {
			t.Fatalf("Compact failed after rejected Rewind: %v", err)
		}
	})
}
