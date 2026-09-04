package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// TestLoopSendsMaxTok1024 proves the effective max_tokens on the wire when
// the Loop builds the request: after STEP 1 it must be 1024 (the
// NABD_MAX_TOKENS default), not the implicit 4096. The request body is
// captured by the httptest server; Authorization is never logged.
func TestLoopSendsMaxTok1024(t *testing.T) {
	var captured struct {
		MaxTokens int `json:"max_tokens"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"جواب"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
	l := &agent.Loop{
		Provider: prov,
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
	}
	if err := l.Run(context.Background(), "مرحبا"); err != nil {
		t.Fatal(err)
	}
	t.Logf("captured max_tokens = %d", captured.MaxTokens)
	if captured.MaxTokens != 1024 {
		t.Errorf("effective max_tokens = %d, want 1024 (NABD_MAX_TOKENS default)", captured.MaxTokens)
	}
}

// noTools satisfies agent.Tools with nothing callable.
type noTools struct{}

func (noTools) Specs() []provider.ToolSpec { return nil }
func (noTools) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	return "", false, nil
}
func (noTools) Check(tool string) (agent.Verdict, string)              { return agent.VerdictDeny, "no" }
func (noTools) Record(tool string, d agent.Decision)                   {}
func (noTools) Effective(tool string, d agent.Decision) agent.Decision { return d }
func (noTools) Ask(ctx context.Context, c agent.ToolCall) agent.Decision {
	return agent.Deny
}
