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

// TestCalibrationEventPerTurn: every successful turn must emit exactly one
// calibration event carrying encoded_bytes, prompt_tokens, and message
// count — the D regression points. It must NOT be a Notice (no model/human
// inflation) and Messages() must ignore it.
func TestCalibrationEventPerTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"جواب"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":500}}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	prov := &provider.OpenAICompat{Key: "k", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}

	var mu sync.Mutex
	var calib []agent.Calibration
	var notices int
	l := &agent.Loop{
		Provider: prov,
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
		Sink: sinkFn3(func(e agent.Event) error {
			mu.Lock()
			defer mu.Unlock()
			if e.Type == agent.EventCalib && e.Calib != nil {
				calib = append(calib, *e.Calib)
			}
			if e.Type == agent.Notice {
				notices++
			}
			return nil
		}),
	}
	if err := l.Run(context.Background(), "سؤال"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calib) != 1 {
		t.Fatalf("calibration events = %d, want exactly 1 per successful turn", len(calib))
	}
	c := calib[0]
	if c.EncodedBytes <= 0 {
		t.Errorf("encoded_bytes = %d, want > 0", c.EncodedBytes)
	}
	if c.PromptTokens != 500 {
		t.Errorf("prompt_tokens = %d, want 500", c.PromptTokens)
	}
	if c.Messages < 1 {
		t.Errorf("messages = %d, want >= 1", c.Messages)
	}
	// The calibration must not inflate the model history as a Notice.
	t.Logf("notices (must be 0 — calibration is journal-only): %d", notices)
}
