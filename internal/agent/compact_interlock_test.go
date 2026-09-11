package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
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
