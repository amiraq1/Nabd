package agent_test

import (
	"context"
	"nabd/internal/event"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// TestLengthCutMarkedInMessage proves two things: a length-cut answer emits
// a Notice, AND the stored assistant text carries a visible cut marker —
// so the next turn cannot build on a truncated answer as if it were whole.
func TestLengthCutMarkedInMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"نص طويل"},"finish_reason":""}]}` + "\n\n"))
		// finish_reason length → normaliseStop → "max_tokens"
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}

	var mu sync.Mutex
	var notices []string
	var events []event.Event
	l := &agent.Loop{
		Provider: prov,
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
		Sink: sinkFn3(func(e event.Event) error {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
			if e.Type == event.Notice {
				notices = append(notices, e.Text)
			}
			return nil
		}),
	}
	if err := l.Run(context.Background(), "اكتب طويلاً"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 {
		t.Fatalf("Notice count = %d, want exactly 1", len(notices))
	}
	// The stored assistant message must contain the cut marker.
	var assistant string
	for _, m := range agent.Messages(event.Live(events)) {
		if m.Role == provider.Assistant && m.Text != "" {
			assistant += m.Text
		}
	}
	if !strings.Contains(assistant, "CUT") {
		t.Errorf("assistant text lacks the cut marker: %q", assistant)
	}
	t.Logf("notice: %q", notices[0])
	t.Logf("assistant text: %q", assistant)
}

type sinkFn3 func(event.Event) error

func (f sinkFn3) Emit(e event.Event) error { return f(e) }
