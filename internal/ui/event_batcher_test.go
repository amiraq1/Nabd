package ui

import (
	"testing"
	"time"

	"nabd/internal/agent"
)

// TestBatcherFlushesBySize verifies that the batcher flushes when maxSize is reached.
func TestBatcherFlushesBySize(t *testing.T) {
	var flushed [][]agent.Event
	b := NewBatcher(time.Hour, 3, func(batch []agent.Event) {
		flushed = append(flushed, batch)
	})
	b.Add(agent.Event{Seq: 1, Type: agent.TextDelta, Text: "a"})
	b.Add(agent.Event{Seq: 2, Type: agent.TextDelta, Text: "b"})
	if len(flushed) != 0 {
		t.Fatalf("expected no flush yet, got %d", len(flushed))
	}
	b.Add(agent.Event{Seq: 3, Type: agent.TextDelta, Text: "c"})
	if len(flushed) != 1 {
		t.Fatalf("expected 1 flush, got %d", len(flushed))
	}
	if len(flushed[0]) != 3 {
		t.Errorf("flushed batch size = %d, want 3", len(flushed[0]))
	}
}

// TestBatcherSensitiveEventFlushesImmediately verifies sensitive events trigger immediate flush.
func TestBatcherSensitiveEventFlushesImmediately(t *testing.T) {
	var flushed [][]agent.Event
	b := NewBatcher(time.Hour, 100, func(batch []agent.Event) {
		flushed = append(flushed, batch)
	})
	b.Add(agent.Event{Seq: 1, Type: agent.TextDelta, Text: "a"})
	b.Add(agent.Event{Seq: 2, Type: agent.TextDelta, Text: "b"})
	if len(flushed) != 0 {
		t.Fatalf("expected no flush yet, got %d", len(flushed))
	}
	// ToolEnd is sensitive.
	b.Add(agent.Event{Seq: 3, Type: agent.ToolEnd})
	if len(flushed) != 1 {
		t.Fatalf("expected immediate flush on sensitive event, got %d", len(flushed))
	}
	if len(flushed[0]) != 3 {
		t.Errorf("flushed batch size = %d, want 3", len(flushed[0]))
	}
}

// TestBatcherStopFlushesRemaining verifies Stop flushes pending events.
func TestBatcherStopFlushesRemaining(t *testing.T) {
	var flushed [][]agent.Event
	b := NewBatcher(time.Hour, 100, func(batch []agent.Event) {
		flushed = append(flushed, batch)
	})
	b.Add(agent.Event{Seq: 1, Type: agent.TextDelta, Text: "a"})
	b.Add(agent.Event{Seq: 2, Type: agent.TextDelta, Text: "b"})
	b.Stop()
	if len(flushed) != 1 {
		t.Fatalf("expected flush on Stop, got %d", len(flushed))
	}
}

// TestBatcherPreservesOrderAndMetadata verifies events keep their order and metadata.
func TestBatcherPreservesOrderAndMetadata(t *testing.T) {
	var flushed [][]agent.Event
	b := NewBatcher(50*time.Millisecond, 100, func(batch []agent.Event) {
		flushed = append(flushed, batch)
	})
	b.Start()
	defer b.Stop()

	events := []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hi"},
		{Seq: 2, Type: agent.TextDelta, Text: "Hello "},
		{Seq: 3, Type: agent.TextDelta, Text: "world"},
		{Seq: 4, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", OK: true}},
	}
	for _, e := range events {
		b.Add(e)
	}

	// Wait for timer flush.
	time.Sleep(80 * time.Millisecond)

	if len(flushed) == 0 {
		t.Fatal("expected at least one flush")
	}
	var got []agent.Event
	for _, batch := range flushed {
		got = append(got, batch...)
	}
	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d", len(got), len(events))
	}
	for i, e := range got {
		if e.Seq != events[i].Seq || e.Type != events[i].Type {
			t.Errorf("event[%d] = {Seq:%d Type:%s}, want {Seq:%d Type:%s}",
				i, e.Seq, e.Type, events[i].Seq, events[i].Type)
		}
	}
}
