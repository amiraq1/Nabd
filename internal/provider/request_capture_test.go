package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUsagePromptTokensParsed proves the provider surfaces usage.prompt_tokens
// on the final chunk, so the budget can be calibrated from a measured value
// instead of an estimate.
func TestUsagePromptTokensParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1234}}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	o := &OpenAICompat{Key: "k", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
	ch, err := o.Stream(context.Background(), Request{
		Messages: []Message{{Role: User, Text: "مرحبا"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got int
	for c := range ch {
		if c.PromptTokens > 0 {
			got = c.PromptTokens
		}
	}
	if got != 1234 {
		t.Errorf("PromptTokens = %d, want 1234", got)
	}
}

// TestCaptureRequestMaxTokens captures the actual request body the OpenAI
// compat provider sends, proving the effective max_tokens value on the
// wire. Authorization is never logged — the header is dropped.
func TestCaptureRequestMaxTokens(t *testing.T) {
	var captured struct {
		MaxTokens int `json:"max_tokens"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never log the Authorization header.
		if r.Header.Get("Authorization") != "" {
			t.Log("auth header present: <REDACTED>")
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		// SSE response: one delta then done.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	o := &OpenAICompat{Key: "test-key", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
	ctx := context.Background()
	ch, err := o.Stream(ctx, Request{
		Messages: []Message{{Role: User, Text: "مرحبا"}},
		MaxTok:   0, // the loop never sets it — this is the current state
	})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	t.Logf("captured max_tokens = %d", captured.MaxTokens)
	if captured.MaxTokens != 4096 {
		t.Errorf("effective max_tokens = %d, want 4096 (the implicit default)", captured.MaxTokens)
	}
}
