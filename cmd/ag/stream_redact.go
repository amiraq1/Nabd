package main

import (
	"nabd/internal/agent"
	"nabd/internal/redact"
)

// streamRedactSink redacts credentials from streamed TextDelta events before
// they reach the sinks, joining chunks so a credential or PEM block split across
// two deltas is redacted as one value (redact.Stream).
//
// One instance wraps the whole Fanout, so the journal, --json, and the UI are
// all redacted by a single pass and no sink carries a second, per-chunk pass.
// It leaves the loop's history untouched: emitLocked appends the original event
// after handing this copy to the sink, so the text sent back to the provider is
// unchanged.
//
// A delta whose redacted output is empty is dropped (never an empty TextDelta),
// and its sequence number is reused by the flush that eventually carries the
// held bytes, so the journal keeps a valid Parent chain (agent.Live stops at a
// missing parent) and no sequence number is assigned twice.
type streamRedactSink struct {
	next    agent.Sink
	enabled bool
	stream  *redact.Stream

	lastEmitted int
	lastDropped int
	droppedPrev bool
}

// newStreamRedactSink wraps next. When journal redaction is off the wrapper
// forwards every event unchanged, matching the existing opt-out.
func newStreamRedactSink(next agent.Sink) *streamRedactSink {
	return &streamRedactSink{
		next:    next,
		enabled: journalRedactionEnabled(),
		stream:  redact.NewStream(nil),
	}
}

func (s *streamRedactSink) Emit(e agent.Event) error {
	if !s.enabled {
		return s.next.Emit(e)
	}

	if e.Type == agent.TextDelta {
		out := s.stream.Write(e.Text)
		if out == "" {
			s.lastDropped = e.Seq
			s.droppedPrev = true
			return nil
		}
		e.Text = out
		if s.droppedPrev {
			e.Parent = s.lastEmitted
			s.droppedPrev = false
		}
		s.lastEmitted = e.Seq
		return s.next.Emit(e)
	}

	// A non-streamed event ends the current text run. Release anything held as
	// its own TextDelta first, reusing the last dropped sequence so the chain is
	// continuous, then fix this event's Parent if it pointed at a dropped delta.
	if s.droppedPrev {
		if held := s.stream.Flush(); held != "" {
			flush := agent.Event{
				Type:   agent.TextDelta,
				Text:   held,
				Seq:    s.lastDropped,
				Parent: s.lastEmitted,
				Time:   e.Time,
			}
			if err := s.next.Emit(flush); err != nil {
				return err
			}
			s.lastEmitted = flush.Seq
		}
		e.Parent = s.lastEmitted
		s.droppedPrev = false
	}
	return s.next.Emit(e)
}

// Sync forwards the durable flush to the wrapped sink, so wrapping the Fanout
// does not lose the per-event durability of permission and mutation records.
func (s *streamRedactSink) Sync() error {
	if d, ok := s.next.(agent.DurableSink); ok {
		return d.Sync()
	}
	return nil
}

// JournalPath reports the journal behind the wrapped sink so a persist error
// still names the file to inspect.
func (s *streamRedactSink) JournalPath() string {
	if p, ok := s.next.(interface{ JournalPath() string }); ok {
		return p.JournalPath()
	}
	if f, ok := s.next.(agent.Fanout); ok {
		for _, c := range f {
			if p, ok := c.(interface{ JournalPath() string }); ok {
				if path := p.JournalPath(); path != "" {
					return path
				}
			}
		}
	}
	return ""
}
