package ui

import (
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"
)

// TestBatcherDeliveryAfterStartupIdle reproduces the failure where an idle period
// immediately after batcher startup consumes the initial one-shot timer without rearming it,
// causing subsequent ordinary (non-sensitive, below maxSize) events to never be delivered.
func TestBatcherDeliveryAfterStartupIdle(t *testing.T) {
	delivered := make(chan []agent.Event, 10)
	emptyFlushed := make(chan struct{}, 10)
	interval := 10 * time.Millisecond

	b := NewBatcher(interval, 100, func(batch []agent.Event) {
		delivered <- batch
	})
	b.emptyFlushHook = func() {
		select {
		case emptyFlushed <- struct{}{}:
		default:
		}
	}

	b.Start()
	defer b.Stop()

	// 1. Establish that a timer expiration has actually processed the empty queue.
	select {
	case <-emptyFlushed:
		// Confirmed: the one-shot timer has expired and Flush ran on an empty queue.
	case <-time.After(2 * time.Second):
		t.Fatal("timer never expired on startup idle")
	}

	// 2. Add an ordinary event (below maxSize, non-sensitive).
	event := agent.Event{Seq: 1, Type: agent.TextDelta, Text: "hello"}
	b.Add(event)

	// 3. Assert onFlush receives it automatically via timer without any Stop, manual Flush,
	// sensitive event, or maxSize trigger.
	select {
	case batch := <-delivered:
		if len(batch) != 1 || batch[0].Seq != 1 {
			t.Fatalf("unexpected batch received: %+v", batch)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ordinary event was never delivered after startup idle period (timer died)")
	}
}

// TestBatcherDeliveryAfterIdlePostDelivery reproduces the failure where an idle period
// following a successful delivery consumes the timer on an empty queue without rearming it,
// causing subsequent ordinary turns to hang indefinitely.
func TestBatcherDeliveryAfterIdlePostDelivery(t *testing.T) {
	delivered := make(chan []agent.Event, 10)
	emptyFlushed := make(chan struct{}, 10)
	interval := 10 * time.Millisecond

	b := NewBatcher(interval, 100, func(batch []agent.Event) {
		delivered <- batch
	})
	b.emptyFlushHook = func() {
		select {
		case emptyFlushed <- struct{}{}:
		default:
		}
	}

	b.Start()
	defer b.Stop()

	// 1. Deliver and observe an initial RunStart batch.
	b.Add(agent.Event{Seq: 1, Type: agent.RunStart, Text: "session started"})

	select {
	case batch := <-delivered:
		if len(batch) != 1 || batch[0].Type != agent.RunStart {
			t.Fatalf("expected initial RunStart batch, got: %+v", batch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initial event was not delivered")
	}

	// 2. Establish that a subsequent empty timer expiration has processed the empty queue.
	select {
	case <-emptyFlushed:
		// Confirmed: after delivering RunStart, the queue emptied and a timer tick ran Flush on empty queue.
	case <-time.After(2 * time.Second):
		t.Fatal("empty timer never expired after initial delivery")
	}

	// 3. Add ordinary events (UserMsg and TextDelta - both non-sensitive, below maxSize).
	b.Add(agent.Event{Seq: 2, Type: agent.UserMsg, Text: "how are you?"})
	b.Add(agent.Event{Seq: 3, Type: agent.TextDelta, Text: "I am fine."})

	// 4. Assert automatic delivery.
	select {
	case batch := <-delivered:
		if len(batch) != 2 || batch[0].Seq != 2 || batch[1].Seq != 3 {
			t.Fatalf("unexpected batch received: %+v", batch)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ordinary events were never delivered after post-delivery idle period (timer died)")
	}
}

// TestBatcherDeliveryMultipleIdleCycles verifies that the batcher can undergo multiple consecutive
// idle cycles (queue empty, timer fires and re-arms) and successfully deliver events during each active period.
func TestBatcherDeliveryMultipleIdleCycles(t *testing.T) {
	delivered := make(chan []agent.Event, 20)
	emptyFlushed := make(chan struct{}, 20)
	interval := 10 * time.Millisecond

	b := NewBatcher(interval, 100, func(batch []agent.Event) {
		delivered <- batch
	})
	b.emptyFlushHook = func() {
		select {
		case emptyFlushed <- struct{}{}:
		default:
		}
	}

	b.Start()
	defer b.Stop()

	seq := 1
	for cycle := 1; cycle <= 3; cycle++ {
		// Wait for an idle empty flush to occur in this cycle
		select {
		case <-emptyFlushed:
		case <-time.After(2 * time.Second):
			t.Fatalf("cycle %d: timer never expired during idle", cycle)
		}

		// Add ordinary event
		expectedSeq := seq
		b.Add(agent.Event{Seq: expectedSeq, Type: agent.TextDelta, Text: "payload"})
		seq++

		// Assert automatic delivery via re-armed timer
		select {
		case batch := <-delivered:
			found := false
			for _, e := range batch {
				if e.Seq == expectedSeq {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("cycle %d: event Seq %d not found in batch: %+v", cycle, expectedSeq, batch)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("cycle %d: event Seq %d never delivered after idle period", cycle, expectedSeq)
		}
	}
}

// TestBatcherNeverEmitsEmptyCallbacks verifies that onFlush is never invoked with an empty slice,
// even when timer fires repeatedly while idle or when Flush() is called directly on an empty queue.
func TestBatcherNeverEmitsEmptyCallbacks(t *testing.T) {
	emptyFlushed := make(chan struct{}, 20)
	var mu sync.Mutex
	emptyBatchesCount := 0
	totalBatchesCount := 0

	interval := 5 * time.Millisecond
	b := NewBatcher(interval, 100, func(batch []agent.Event) {
		mu.Lock()
		defer mu.Unlock()
		totalBatchesCount++
		if len(batch) == 0 {
			emptyBatchesCount++
		}
	})
	b.emptyFlushHook = func() {
		select {
		case emptyFlushed <- struct{}{}:
		default:
		}
	}

	b.Start()

	// Let timer expire empty multiple times
	for i := 0; i < 5; i++ {
		select {
		case <-emptyFlushed:
		case <-time.After(2 * time.Second):
			t.Fatalf("empty timer tick %d never fired", i)
		}
	}

	// Call Flush directly multiple times while empty
	for i := 0; i < 5; i++ {
		b.Flush()
	}

	b.Stop()

	mu.Lock()
	defer mu.Unlock()
	if totalBatchesCount > 0 || emptyBatchesCount > 0 {
		t.Fatalf("expected 0 batches emitted for empty queue, got %d (empty batches: %d)", totalBatchesCount, emptyBatchesCount)
	}
}

// TestBatcherStopFlushesPendingWithoutTimerResurrection verifies that Stop() flushes pending events
// and terminates the loop without re-arming or resurrecting the timer.
func TestBatcherStopFlushesPendingWithoutTimerResurrection(t *testing.T) {
	delivered := make(chan []agent.Event, 10)
	interval := 10 * time.Millisecond

	b := NewBatcher(interval, 100, func(batch []agent.Event) {
		delivered <- batch
	})

	b.Start()

	// Add an event
	b.Add(agent.Event{Seq: 1, Type: agent.TextDelta, Text: "pending"})

	// Stop immediately to flush pending
	b.Stop()

	// Expect delivered batch
	select {
	case batch := <-delivered:
		if len(batch) != 1 || batch[0].Seq != 1 {
			t.Fatalf("expected batch with Seq 1, got %+v", batch)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pending event was not flushed upon Stop()")
	}

	// Verify state
	b.mu.Lock()
	stopped := b.stopped
	b.mu.Unlock()
	if !stopped {
		t.Fatal("batcher should be marked stopped")
	}

	// Further Add should be ignored and no panic
	b.Add(agent.Event{Seq: 2, Type: agent.TextDelta, Text: "after stop"})
	b.Flush()

	select {
	case batch := <-delivered:
		t.Fatalf("unexpected batch delivered after stop: %+v", batch)
	case <-time.After(50 * time.Millisecond):
		// Expected: nothing delivered
	}
}

// TestBatcherConcurrentAddFlushStop verifies that concurrent Add, Flush, and Stop operations
// do not cause race conditions, deadlocks, or panics.
func TestBatcherConcurrentAddFlushStop(t *testing.T) {
	var mu sync.Mutex
	var allDelivered []agent.Event

	interval := 5 * time.Millisecond
	b := NewBatcher(interval, 50, func(batch []agent.Event) {
		mu.Lock()
		allDelivered = append(allDelivered, batch...)
		mu.Unlock()
	})

	b.Start()

	var wg sync.WaitGroup
	numProducers := 10
	eventsPerProducer := 50

	// Concurrent producers
	for p := 0; p < numProducers; p++ {
		wg.Add(1)
		go func(pID int) {
			defer wg.Done()
			for i := 0; i < eventsPerProducer; i++ {
				seq := pID*1000 + i
				evType := agent.TextDelta
				if i%10 == 0 {
					evType = agent.ToolStart
				}
				b.Add(agent.Event{Seq: seq, Type: evType, Text: "data"})
				time.Sleep(100 * time.Microsecond)
			}
		}(p)
	}

	// Concurrent flushers
	stopFlushers := make(chan struct{})
	for f := 0; f < 3; f++ {
		go func() {
			for {
				select {
				case <-stopFlushers:
					return
				default:
					b.Flush()
					time.Sleep(500 * time.Microsecond)
				}
			}
		}()
	}

	// Wait for all producers to finish adding
	wg.Wait()
	close(stopFlushers)

	// Stop batcher (which does final flush)
	b.Stop()

	mu.Lock()
	count := len(allDelivered)
	mu.Unlock()

	expectedTotal := numProducers * eventsPerProducer
	if count != expectedTotal {
		t.Fatalf("expected %d events delivered, got %d", expectedTotal, count)
	}
}
