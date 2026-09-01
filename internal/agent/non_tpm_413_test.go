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

// TestNonTPM413NoFalseNotice: a 413 whose body is NOT a TPM violation
// (no "Limit"/"Requested") must not emit a "سقف الدقيقة: 0 · المطلوب 0"
// Notice — a lie is worse than silence. It falls through to the generic
// httpError path.
func TestNonTPM413NoFalseNotice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		w.Write([]byte(`{"error":{"message":"payload too large: 5 MB limit exceeded"}}`))
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
	for _, n := range notices {
		if n == "سقف الدقيقة: 0 · المطلوب 0 · انتظر ثم أعد" {
			t.Fatalf("false TPM notice emitted for a non-TPM 413: %q", n)
		}
	}
	if len(notices) != 0 {
		t.Logf("notices (none expected to be TPM): %v", notices)
	}
}
