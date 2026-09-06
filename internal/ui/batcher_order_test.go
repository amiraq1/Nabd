package ui

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nabd/internal/agent"
)

// TestBatcherStopIsIdempotent: calling Stop twice must not panic.
func TestBatcherStopIsIdempotent(t *testing.T) {
	b := NewBatcher(time.Hour, 10, func([]agent.Event) {})
	b.Start()

	// First stop
	b.Stop()

	// Second stop must not panic (e.g. close of closed channel)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second Stop() panicked: %v", r)
		}
	}()
	b.Stop()
}

// TestBatcherStopBeforeStartDoesNotPanic: stopping an unstarted batcher must be safe.
func TestBatcherStopBeforeStartDoesNotPanic(t *testing.T) {
	b := NewBatcher(time.Hour, 10, func([]agent.Event) {})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Stop() before Start() panicked: %v", r)
		}
	}()
	b.Stop()
}

// TestBatcherOnFlushNeverRunsConcurrently: onFlush must never execute concurrently with itself.
func TestBatcherOnFlushNeverRunsConcurrently(t *testing.T) {
	var inFlush int32
	var concurrentCount int32
	var enteredOnce sync.Once
	flushEntered := make(chan struct{})
	flushRelease := make(chan struct{})

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(flushRelease)
		})
	}
	defer release()

	b := NewBatcher(time.Hour, 2, func(batch []agent.Event) {
		cur := atomic.AddInt32(&inFlush, 1)
		if cur > 1 {
			atomic.AddInt32(&concurrentCount, 1)
		}
		enteredOnce.Do(func() {
			close(flushEntered)
		})
		<-flushRelease
		atomic.AddInt32(&inFlush, -1)
	})

	// Add 2 events to trigger maxSize flush in Goroutine 1
	doneG1 := make(chan struct{})
	go func() {
		defer close(doneG1)
		b.Add(agent.Event{Seq: 1, Type: agent.TextDelta})
		b.Add(agent.Event{Seq: 2, Type: agent.TextDelta})
	}()

	// Wait until Goroutine 1 enters onFlush
	select {
	case <-flushEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first flush did not enter callback")
	}

	// While Goroutine 1 is inside onFlush, Goroutine 2 adds a sensitive event
	startedG2 := make(chan struct{})
	doneG2 := make(chan struct{})
	go func() {
		defer close(doneG2)
		close(startedG2)
		b.Add(agent.Event{Seq: 3, Type: agent.PermAsk})
	}()

	select {
	case <-startedG2:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine 2 did not start")
	}

	// Let Goroutine 2 attempt to flush while Goroutine 1 is inside onFlush
	select {
	case <-doneG2:
	case <-time.After(20 * time.Millisecond):
	}

	// Release all flushes
	release()

	select {
	case <-doneG1:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine 1 did not finish")
	}

	select {
	case <-doneG2:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine 2 did not finish")
	}

	if got := atomic.LoadInt32(&concurrentCount); got > 0 {
		t.Fatalf("onFlush ran concurrently with itself: count=%d", got)
	}
}

// TestBatcherAddAfterStopDoesNotPanic
func TestBatcherAddAfterStopDoesNotPanic(t *testing.T) {
	b := NewBatcher(time.Hour, 10, func([]agent.Event) {})
	b.Start()
	b.Stop()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Add after Stop panicked: %v", r)
		}
	}()
	b.Add(agent.Event{Seq: 1, Type: agent.TextDelta})
}

// TestBatcherPreservesGlobalOrderAcrossTimerAndSensitiveFlush
func TestBatcherPreservesGlobalOrderAcrossTimerAndSensitiveFlush(t *testing.T) {
	var mu sync.Mutex
	var deliveredSeqs []int

	b := NewBatcher(5*time.Millisecond, 100, func(batch []agent.Event) {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range batch {
			deliveredSeqs = append(deliveredSeqs, e.Seq)
		}
	})
	b.Start()

	// Produce interleaving of regular and sensitive events
	total := 100
	for i := 1; i <= total; i++ {
		evType := agent.TextDelta
		if i%10 == 0 {
			evType = agent.ToolStart
		}
		b.Add(agent.Event{Seq: i, Type: evType})
	}
	b.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(deliveredSeqs) != total {
		t.Fatalf("delivered %d events, want %d", len(deliveredSeqs), total)
	}

	for i := 0; i < len(deliveredSeqs); i++ {
		if deliveredSeqs[i] != i+1 {
			t.Fatalf("order violated at index %d: got Seq %d, want %d", i, deliveredSeqs[i], i+1)
		}
	}
}
