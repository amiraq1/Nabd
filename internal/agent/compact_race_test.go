package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

const testCompactTarget = 50

func newTestLoop(t *testing.T) *Loop {
	return &Loop{
		Provider: mockProvider{chunks: []provider.Chunk{
			{Kind: provider.ChunkText, Text: "reply"},
			{Kind: provider.ChunkStop, Stop: "end_turn"},
		}},
		Budget: NewBudget(),
	}
}

func seedTurns(t *testing.T, l *Loop, count int) {
	for i := 1; i <= count; i++ {
		text := fmt.Sprintf("turn %d", i)
		if i == 1 {
			text = strings.Repeat("bulk-turn-one-text-", 100)
		}
		l.emit(Event{Type: UserMsg, Text: text})
	}
}

// boundaryDroppingProvider removes the chosen boundary from the live branch
// from inside Stream — i.e. during Compact's Phase 2, while Compact holds
// historyMu.
//
// This is now the only way to reach Compact's Phase-3 staleness check. Once
// historyMu serialises history mutation, a real concurrent /rewind is rejected
// with ErrHistoryMutationInProgress and can no longer move the boundary, so the
// original rejects_boundary_dropped_by_concurrent_rewind scenario is
// unreachable in production. The check is retained because it is the only thing
// standing between a stale FirstKept and a collapsed live projection, and this
// provider keeps it covered without a production test hook.
type boundaryDroppingProvider struct {
	l      *Loop
	cutSeq int // drop every live event whose Seq >= cutSeq
	once   sync.Once
	fired  atomic.Bool
}

func (p *boundaryDroppingProvider) Name() string { return "boundary-dropping" }

func (p *boundaryDroppingProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.once.Do(func() {
		p.l.mu.Lock()
		kept := make([]Event, 0, len(p.l.hist))
		for _, e := range p.l.hist {
			if e.Seq < p.cutSeq {
				kept = append(kept, e)
			}
		}
		p.l.hist = kept
		p.l.mu.Unlock()
		p.fired.Store(true)
	})

	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Kind: provider.ChunkText, Text: "synthetic summary"}
	close(ch)
	return ch, nil
}

func TestCompactRejectsBoundaryDroppedDuringSummarisation(t *testing.T) {
	l := newTestLoop(t) // reuse the existing helper in this file

	seedTurns(t, l, 4) // must produce >= 2 UserMsg events so chooseBoundary succeeds

	l.mu.Lock()
	live := Live(l.hist)
	l.mu.Unlock()

	firstKept, _, ok := chooseBoundaryWith(live, testCompactTarget, l.estimateMessages)
	if !ok {
		t.Fatalf("precondition: chooseBoundaryWith found no boundary in %d events", len(live))
	}

	// Survivor: an event strictly older than the boundary. It must still be on
	// the branch afterwards — its disappearance is the live-projection collapse
	// this check exists to prevent.
	survivorSeq := live[0].Seq
	if survivorSeq >= firstKept {
		t.Fatalf("precondition: survivor seq %d not older than boundary %d", survivorSeq, firstKept)
	}

	p := &boundaryDroppingProvider{l: l, cutSeq: firstKept}
	l.Provider = p

	err := l.Compact(context.Background(), testCompactTarget)

	if !p.fired.Load() {
		t.Fatal("provider never ran: Compact returned before Phase 2, test proves nothing")
	}
	if !errors.Is(err, ErrCompactBoundaryStale) {
		t.Errorf("Compact err = %v, want ErrCompactBoundaryStale", err)
	}

	l.mu.Lock()
	after := Live(l.hist)
	l.mu.Unlock()

	for _, e := range after {
		if e.Type == Compact {
			t.Errorf("a stale Compact event was appended (FirstKept=%d)", e.FirstKept)
		}
	}

	found := false
	seqs := make([]int, 0, len(after))
	for _, e := range after {
		seqs = append(seqs, e.Seq)
		if e.Seq == survivorSeq {
			found = true
		}
	}
	if !found {
		t.Errorf("survivor seq %d vanished; live seqs = %v", survivorSeq, seqs)
	}
}
