package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// TestLengthNoticeEmitted proves that when the provider stops with
// finish_reason=length (which MaxTok=1024 makes likely for long answers),
// a Notice reaches the event channel — the user must not get a silently
// truncated answer.
func TestLengthNoticeEmitted(t *testing.T) {
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
	l := &agent.Loop{
		Provider: prov,
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
		Sink:     sinkFn3(func(e agent.Event) error { mu.Lock(); defer mu.Unlock(); if e.Type == agent.Notice { notices = append(notices, e.Text) }; return nil }),
	}
	if err := l.Run(context.Background(), "اكتب طويلاً"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 {
		t.Fatalf("Notice count = %d, want exactly 1 for length-truncated answer", len(notices))
	}
	t.Logf("notice: %q", notices[0])
}

type sinkFn3 func(agent.Event) error

func (f sinkFn3) Emit(e agent.Event) error { return f(e) }
