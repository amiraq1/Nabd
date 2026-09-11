package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nabd/internal/provider"
)

func TestCompactInterlockRejectsConcurrentHistoryMutation(t *testing.T) {
	ready := make(chan struct{}, 1)
	unblock := make(chan struct{})
	prov := &readyBlockingProvider{readyCh: ready, unblock: unblock}
	l := &Loop{Provider: prov, Budget: NewBudget()}

	l.emit(Event{Type: UserMsg, Text: strings.Repeat("large first turn ", 120)})
	l.emit(Event{Type: UserMsg, Text: "short second turn"})

	compactErr := make(chan error, 1)
	go func() {
		compactErr <- l.Compact(context.Background(), 50)
	}()
	<-ready

	before := l.Hist()
	if _, err := l.Rewind(1); !errors.Is(err, ErrHistoryMutationInProgress) {
		t.Fatalf("Rewind during Compact error = %v, want ErrHistoryMutationInProgress", err)
	}
	if got := l.Hist(); len(got) != len(before) {
		t.Fatalf("rejected Rewind changed history length: %d -> %d", len(before), len(got))
	}

	if err := l.Compact(context.Background(), 50); !errors.Is(err, ErrHistoryMutationInProgress) {
		t.Fatalf("second Compact error = %v, want ErrHistoryMutationInProgress", err)
	}

	close(unblock)
	if err := <-compactErr; err != nil {
		t.Fatalf("first Compact failed: %v", err)
	}

	if _, err := l.Rewind(1); err != nil {
		t.Fatalf("interlock was not released after Compact: %v", err)
	}
}

func TestCompactInterlockReleasesAfterEarlyFailure(t *testing.T) {
	l := &Loop{Budget: NewBudget()}
	if err := l.Compact(context.Background(), 50); err == nil {
		t.Fatal("Compact without a boundary unexpectedly succeeded")
	}

	l.emit(Event{Type: UserMsg, Text: "turn after failed compact"})
	if _, err := l.Rewind(1); err != nil {
		t.Fatalf("interlock remained held after early failure: %v", err)
	}
}

func seedTurnsUntilPressure(t *testing.T, l *Loop, targetPressure float64) {
	l.EstimateMessages = func(ms []provider.Message) int {
		return int(float64(l.Budget.Usable()) * targetPressure)
	}
	l.emit(Event{Type: UserMsg, Text: "prior turn"})
}

func captureNotices(t *testing.T, l *Loop) func() []string {
	start := len(l.Hist())
	return func() []string {
		var out []string
		for _, e := range l.Hist()[start:] {
			if e.Type == Notice {
				out = append(out, e.Text)
			}
		}
		return out
	}
}

func TestAutoCompactSkipsSilentlyWhenHistoryMutationInProgress(t *testing.T) {
	l := newTestLoop(t)

	// Drive pressure above the auto-compaction threshold (0.75).
	seedTurnsUntilPressure(t, l, 0.89)

	// Hold historyMu for the duration of the turn so the auto-compaction
	// attempt inside Run hits TryLock and fails with
	// ErrHistoryMutationInProgress. defer guarantees release even if an
	// assertion below aborts the test.
	if !l.historyMu.TryLock() {
		t.Fatal("precondition: historyMu already held")
	}
	defer l.historyMu.Unlock()

	notices := captureNotices(t, l)

	if err := l.Run(context.Background(), "next turn"); err != nil {
		t.Fatalf("Run returned %v; a busy history lock must not fail the turn", err)
	}

	for _, n := range notices() {
		if strings.Contains(n, "compact") {
			t.Errorf("auto-compaction surfaced a notice while history was locked: %q", n)
		}
	}
}
