package agent

import (
	"sync"
	"testing"
)

type sliceSink struct {
	mu  sync.Mutex
	evs []Event
}

func (s *sliceSink) Emit(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evs = append(s.evs, e)
	return nil
}

func TestEmitRaceOrder(t *testing.T) {
	sink := &sliceSink{}
	l := &Loop{Sink: sink}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.emit(Event{Type: TextDelta, Text: "test"})
		}(i)
	}
	wg.Wait()

	l.mu.Lock()
	hist := l.hist
	l.mu.Unlock()

	if len(hist) != len(sink.evs) {
		t.Fatalf("length mismatch: hist=%d, sink=%d", len(hist), len(sink.evs))
	}

	for i := range hist {
		if hist[i].Seq != sink.evs[i].Seq {
			t.Errorf("mismatch at index %d: hist seq=%d, sink seq=%d", i, hist[i].Seq, sink.evs[i].Seq)
		}
	}
}
