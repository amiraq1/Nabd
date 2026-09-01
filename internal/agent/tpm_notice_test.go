package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// TestTPMLimitNoticeEmitted: a provider 413 (Groq's per-minute TPM
// violation) must produce a human Notice naming the requested count, so a
// transient rate hit does not look like a final failure.
func TestTPMLimitNoticeEmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusTooManyRequests)
		// NOTE: Groq returns 413 for TPM violations; the status line here is
		// incidental — the detection is by message shape, matching the
		// journal's recorded errors.
		w.Write([]byte(`{"error":{"message":"Request too large for model on tokens per minute (TPM): Limit 8000, Requested 8123, please reduce"}}`))
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
	_ = l.Run(context.Background(), "سؤال")

	mu.Lock()
	defer mu.Unlock()
	var foundN bool
	for _, n := range notices {
		if strings.Contains(n, "8123") {
			foundN = true
		}
	}
	if !foundN {
		t.Errorf("no Notice carried the requested count 8123; notices=%v", notices)
	}
}
