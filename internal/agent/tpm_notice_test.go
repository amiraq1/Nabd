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
// (from the recorded sessions) must produce a Notice carrying both Limit
// and Requested, and emit them as event fields.
func TestTPMLimitNoticeEmitted(t *testing.T) {
	// Real body shape from session 20260901-133251.jsonl.
	groqBody := "Request too large for model `openai/gpt-oss-20b` in organization `org_x` service tier `on_demand` on tokens per minute (TPM): Limit 8000, Requested 8968, please reduce your message size and try again."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge) // 413 — what Groq sends
		w.Write([]byte(`{"error":{"message":"` + groqBody + `"}}`))
	}))
	defer srv.Close()

	prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}

	var mu sync.Mutex
	var notices []agent.Event
	l := &agent.Loop{
		Provider: prov,
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
		Sink: sinkFn3(func(e agent.Event) error {
			mu.Lock()
			defer mu.Unlock()
			if e.Type == agent.Notice {
				notices = append(notices, e)
			}
			return nil
		}),
	}
	if err := l.Run(context.Background(), "سؤال"); err == nil {
		t.Fatal("expected a run error after the 413")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 {
		t.Fatalf("Notice count = %d, want exactly 1", len(notices))
	}
	n := notices[0]
	if !strings.Contains(n.Text, "8000") || !strings.Contains(n.Text, "8968") {
		t.Errorf("Notice must carry Limit and Requested, got %q", n.Text)
	}
	if n.Limit != 8000 || n.Requested != 8968 {
		t.Errorf("event fields: Limit=%d Requested=%d, want 8000 and 8968", n.Limit, n.Requested)
	}
}
