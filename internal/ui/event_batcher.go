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
	timer     *time.Timer
	done      chan struct{}
}

// NewBatcher creates a batcher with the given configuration.
func NewBatcher(interval time.Duration, maxSize int, onFlush func([]agent.Event)) *Batcher {
	b := &Batcher{
		interval: interval,
		maxSize:  maxSize,
		onFlush:  onFlush,
		done:     make(chan struct{}),
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

// Start begins the background flush loop.
func (b *Batcher) Start() {
	b.mu.Lock()
	b.timer = time.NewTimer(b.interval)
	b.mu.Unlock()
	go b.loop()
}

// Stop terminates the batcher and flushes any remaining events.
func (b *Batcher) Stop() {
	close(b.done)
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.mu.Unlock()
	b.Flush()
}

// Add appends an event. Sensitive events trigger an immediate flush.
func (b *Batcher) Add(e agent.Event) {
	b.mu.Lock()
	b.events = append(b.events, e)
	shouldFlush := b.sensitive[e.Type] || len(b.events) >= b.maxSize
	b.mu.Unlock()
	if shouldFlush {
		b.Flush()
	}
}

// Flush sends the current batch and resets. Safe for concurrent use.
func (b *Batcher) Flush() {
	b.mu.Lock()
	if len(b.events) == 0 {
		b.mu.Unlock()
		return
	}
	batch := make([]agent.Event, len(b.events))
	copy(batch, b.events)
	b.events = b.events[:0]
	if b.timer != nil {
		b.timer.Stop()
		b.timer.Reset(b.interval)
	}
	b.mu.Unlock()
	if b.onFlush != nil {
		b.onFlush(batch)
	}
}

// loop waits for timer ticks.
func (b *Batcher) loop() {
	for {
		b.mu.Lock()
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
