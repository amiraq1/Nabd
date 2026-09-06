package ui

import (
	"sync"
	"time"

	"nabd/internal/agent"
)

// Batcher collects agent events into batches to avoid sending one Bubble Tea
// message per text_delta. It flushes when either a time window elapses or a
// size limit is reached. Sensitive events trigger an immediate flush.
type Batcher struct {
	mu        sync.Mutex
	events    []agent.Event
	interval  time.Duration
	maxSize   int
	sensitive map[agent.EventType]bool
	onFlush   func([]agent.Event)

	startOnce sync.Once
	stopOnce  sync.Once
	started   bool
	stopped   bool
	done      chan struct{}
	loopDone  chan struct{}
	flushMu   sync.Mutex
	timer     *time.Timer
}

// NewBatcher creates a batcher with the given configuration.
func NewBatcher(interval time.Duration, maxSize int, onFlush func([]agent.Event)) *Batcher {
	b := &Batcher{
		interval: interval,
		maxSize:  maxSize,
		onFlush:  onFlush,
		done:     make(chan struct{}),
		loopDone: make(chan struct{}),
	}
	b.sensitive = map[agent.EventType]bool{
		agent.PermAsk:     true,
		agent.PermReply:   true,
		agent.ToolStart:   true,
		agent.ToolEnd:     true,
		agent.RunError:    true,
		agent.Interrupted: true,
		agent.RunEnd:      true,
	}
	return b
}

// Start begins the background flush loop. It is safe to call Start multiple times.
func (b *Batcher) Start() {
	b.startOnce.Do(func() {
		b.mu.Lock()
		if b.stopped {
			b.mu.Unlock()
			return
		}
		b.started = true
		b.timer = time.NewTimer(b.interval)
		b.mu.Unlock()
		go b.loop()
	})
}

// Stop terminates the batcher and flushes any remaining events.
// It is idempotent and safe to call multiple times or before Start.
func (b *Batcher) Stop() {
	b.stopOnce.Do(func() {
		b.mu.Lock()
		b.stopped = true
		wasStarted := b.started
		if b.timer != nil {
			b.timer.Stop()
		}
		b.mu.Unlock()

		close(b.done)
		if wasStarted {
			<-b.loopDone
		}
		b.Flush()
	})
}

// Add appends an event. Sensitive events trigger an immediate flush.
// If the batcher is stopped, Add is a safe no-op.
func (b *Batcher) Add(e agent.Event) {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	b.events = append(b.events, e)
	shouldFlush := b.sensitive[e.Type] || len(b.events) >= b.maxSize
	b.mu.Unlock()

	if shouldFlush {
		b.Flush()
	}
}

// Flush sends the current batch and resets the timer. Safe for concurrent use.
// Batches are delivered strictly in order and onFlush is never called concurrently.
func (b *Batcher) Flush() {
	b.flushMu.Lock()
	defer b.flushMu.Unlock()

	b.mu.Lock()
	if len(b.events) == 0 {
		b.mu.Unlock()
		return
	}
	batch := make([]agent.Event, len(b.events))
	copy(batch, b.events)
	b.events = b.events[:0]
	b.resetTimerLocked()
	b.mu.Unlock()

	if b.onFlush != nil {
		b.onFlush(batch)
	}
}

// resetTimerLocked stops and resets the flush timer if active.
// Caller must hold b.mu.
func (b *Batcher) resetTimerLocked() {
	if b.timer == nil || !b.started || b.stopped {
		return
	}
	if !b.timer.Stop() {
		select {
		case <-b.timer.C:
		default:
		}
	}
	b.timer.Reset(b.interval)
}

// loop waits for timer ticks or the stop signal.
func (b *Batcher) loop() {
	defer close(b.loopDone)
	for {
		b.mu.Lock()
		if b.stopped {
			b.mu.Unlock()
			return
		}
		ch := b.timer.C
		b.mu.Unlock()

		select {
		case <-b.done:
			return
		case <-ch:
			b.Flush()
		}
	}
}
