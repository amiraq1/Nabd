package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIncludeUsageRequested: the calibration event reads usage.prompt_tokens
// on the final chunk, but Groq/OpenAI only send usage in a streaming response
// when the request asks for it via stream_options.include_usage. Without this
// field on the wire, prompt_tokens stays 0 and the ratio is never measured.
func TestIncludeUsageRequested(t *testing.T) {
	var captured struct {
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	o := &OpenAICompat{Key: "k", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
	ch, err := o.Stream(context.Background(), Request{Messages: []Message{{Role: User, Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if captured.StreamOptions == nil || !captured.StreamOptions.IncludeUsage {
		t.Fatal("request body must include stream_options.include_usage so the server returns usage.prompt_tokens for calibration")
	}
}
