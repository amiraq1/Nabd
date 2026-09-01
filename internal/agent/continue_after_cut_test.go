package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// captureServer records the raw request bodies it receives and answers with
// a normal end_turn, so a second Run can be driven against history.
func captureServer(t *testing.T, bodies *[][]byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		raw := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			raw = append(raw, buf[:n]...)
			if err != nil {
				break
			}
		}
		*bodies = append(*bodies, raw)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"جواب"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
}

// cutProvider ends the first stream with finish_reason=length.
type cutProvider struct{}

func (cutProvider) Name() string { return "cut" }

func (cutProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 3)
	ch <- provider.Chunk{Kind: provider.ChunkText, Text: "إجابة مقطوعة "}
	ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "max_tokens"}
	close(ch)
	return ch, nil
}

// TestContinueAfterLengthCut: run 1 ends with a length cut (marker in the
// stored assistant text). Run 2 ("أكمل") must be accepted by the provider —
// the history carries a truncated assistant message, and that must not
// break the protocol or be silently dropped.
func TestContinueAfterLengthCut(t *testing.T) {
	var bodies [][]byte
	srv := captureServer(t, &bodies)
	defer srv.Close()

	// First run: the cut provider emits length.
	l := &agent.Loop{
		Provider: cutProvider{},
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
	}
	if err := l.Run(context.Background(), "اكتب طويلاً"); err != nil {
		t.Fatal(err)
	}

	// Second run against the same loop (history has the cut marker), with a
	// capture server standing in for the provider.
	l.Provider = &provider.OpenAICompat{Key: "k", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
	if err := l.Run(context.Background(), "أكمل"); err != nil {
		t.Fatal(err)
	}

	// The second request's history must contain the assistant cut marker.
	if len(bodies) == 0 {
		t.Fatal("second run made no request")
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(bodies[len(bodies)-1], &req); err != nil {
		t.Fatalf("decode second request: %v", err)
	}
	found := false
	for _, m := range req.Messages {
		if m.Role == "assistant" && strings.Contains(m.Content, "مُقتطَع") {
			found = true
		}
	}
	if !found {
		t.Error("second request history lacks the cut marker in the assistant message")
	}
}
