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

// TestTPMLimitNoticeEmitted: a provider 413 with the real Groq body shape
// must produce exactly one Notice carrying both Limit and Requested, emit
// them as event fields, end the round with exactly one terminal marker, and
// leave the session usable for a next request — the 413 is transient, not
// final.
func TestTPMLimitNoticeEmitted(t *testing.T) {
	// Real body shape from session 20260901-133251.jsonl.
	groqBody := "Request too large for model `openai/gpt-oss-20b` in organization `org_x` service tier `on_demand` on tokens per minute (TPM): Limit 8000, Requested 8968, please reduce your message size and try again."

	// The provider answers the first request with 413, then works normally.
	var muCalls sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		muCalls.Lock()
		calls++
		first := calls == 1
		muCalls.Unlock()
		if first {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge) // 413 — what Groq sends
			w.Write([]byte(`{"error":{"message":"` + groqBody + `"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"جواب"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}

	var mu sync.Mutex
	var notices []agent.Event
	var all []agent.Event
	l := &agent.Loop{
		Provider: prov,
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
		Sink: sinkFn3(func(e agent.Event) error {
			mu.Lock()
			defer mu.Unlock()
			all = append(all, e)
			if e.Type == agent.Notice {
				notices = append(notices, e)
			}
			return nil
		}),
	}

	// Run 1: the 413 fires. It must NOT look like a silent final failure.
	if err := l.Run(context.Background(), "سؤال"); err == nil {
		t.Fatal("expected a run error after the 413")
	}

	mu.Lock()
	if len(notices) != 1 {
		mu.Unlock()
		t.Fatalf("Notice count = %d, want exactly 1", len(notices))
	}
	n := notices[0]
	if !strings.Contains(n.Text, "8000") || !strings.Contains(n.Text, "8968") {
		t.Errorf("Notice must carry Limit and Requested, got %q", n.Text)
	}
	if n.Limit != 8000 || n.Requested != 8968 {
		t.Errorf("event fields: Limit=%d Requested=%d, want 8000 and 8968", n.Limit, n.Requested)
	}
	// Exactly one terminal marker (RunError) for run 1.
	terminals := 0
	for _, e := range all {
		if e.Type == agent.RunError {
			terminals++
		}
	}
	all = nil
	notices = nil
	mu.Unlock()
	if terminals != 1 {
		t.Errorf("terminal markers after 413 = %d, want exactly 1", terminals)
	}

	// Run 2: the session must accept a next request (the 413 is transient).
	if err := l.Run(context.Background(), "متابعة"); err != nil {
		t.Fatalf("second run failed — session did not survive the 413: %v", err)
	}
}
