package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHTTP429ResumeWithHTTPTest asserts that an HTTP 429 response triggers a bounded
// retry, parses the wait duration from the provider body, emits ChunkRateLimit with
// the exact counts, does not mutate the wire request body, and completes successfully.
func TestHTTP429ResumeWithHTTPTest(t *testing.T) {
	var (
		mu       sync.Mutex
		requests [][]byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		callNum := len(requests)
		mu.Unlock()

		if callNum == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached for model qwen/qwen3.8-27b on tokens per minute (TPM): Limit 8000, Used 1859, Requested 6778. Please try again in 0.05s."}}`))
			return
		}

		// Second attempt succeeds with SSE stream
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"success from retry\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	prov := &OpenAICompat{
		BaseURL: server.URL,
		Key:     "test-key",
		Model:   "test-model",
		Client:  server.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := prov.Stream(ctx, Request{
		Messages: []Message{{Role: User, Text: "test 429 resume"}},
		MaxTok:   1024,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var (
		rateLimitChunks []*RateLimitInfo
		textReceived    string
		completed       bool
	)

	for c := range ch {
		switch c.Kind {
		case ChunkRateLimit:
			rateLimitChunks = append(rateLimitChunks, c.RateLimit)
		case ChunkText:
			textReceived += c.Text
		case ChunkStop:
			completed = true
		case ChunkError:
			t.Fatalf("unexpected ChunkError: %v", c.Err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// 1. Retry occurred exactly once
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests (1 retry), got %d", len(requests))
	}

	// 2. Wait was correctly calculated
	if len(rateLimitChunks) != 1 {
		t.Fatalf("expected exactly 1 ChunkRateLimit, got %d", len(rateLimitChunks))
	}
	rl := rateLimitChunks[0]
	if rl.Code != 429 {
		t.Errorf("expected code 429, got %d", rl.Code)
	}
	if rl.Limit != 8000 || rl.Used != 1859 || rl.Requested != 6778 {
		t.Errorf("counts mismatch: limit=%d used=%d requested=%d", rl.Limit, rl.Used, rl.Requested)
	}
	if rl.WaitSec < 0.04 || rl.WaitSec > 0.06 {
		t.Errorf("wait_s not parsed from body: got %f", rl.WaitSec)
	}
	if rl.Attempt != 1 {
		t.Errorf("expected attempt 1, got %d", rl.Attempt)
	}

	// 3. Operation completed successfully
	if !completed {
		t.Errorf("stream did not complete with ChunkStop")
	}
	if textReceived != "success from retry" {
		t.Errorf("unexpected text received: %q", textReceived)
	}

	// 4. No message mutation: request 2 body byte-for-byte identical to request 1 body
	if !bytes.Equal(requests[0], requests[1]) {
		t.Errorf("request body was mutated between attempts:\nreq1: %s\nreq2: %s",
			string(requests[0]), string(requests[1]))
	}
}

// TestHTTP429MaxAttemptsExceeded asserts that repeated 429 errors terminate
// after max429Attempts without infinite looping.
func TestHTTP429MaxAttemptsExceeded(t *testing.T) {
	var reqCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"TPM limit exceeded. Please try again in 0.01s."}}`))
	}))
	defer server.Close()

	prov := &OpenAICompat{
		BaseURL: server.URL,
		Key:     "test-key",
		Model:   "test-model",
		Client:  server.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := prov.Stream(ctx, Request{
		Messages: []Message{{Role: User, Text: "infinite loop test"}},
		MaxTok:   512,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var (
		sawError bool
		rlCount  int
	)
	for c := range ch {
		if c.Kind == ChunkRateLimit {
			rlCount++
		}
		if c.Kind == ChunkError {
			sawError = true
		}
	}

	if !sawError {
		t.Fatalf("expected ChunkError when max attempts exceeded")
	}
	// max429Attempts is 3, so reqCount must be exactly 3
	if n := reqCount.Load(); n != 3 {
		t.Fatalf("expected bounded attempts = 3, got %d", n)
	}
	if rlCount != 2 {
		t.Fatalf("expected 2 rate limit events before final failure, got %d", rlCount)
	}
}

// TestHTTP429RetryAfterHeaderParsing asserts that Retry-After header takes precedence
// over body and supports floating-point or integer durations.
func TestHTTP429RetryAfterHeaderParsing(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0.03")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached. Please try again in 100s."}}`))
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	prov := &OpenAICompat{
		BaseURL: server.URL,
		Key:     "test-key",
		Model:   "test-model",
		Client:  server.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := prov.Stream(ctx, Request{
		Messages: []Message{{Role: User, Text: "header test"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var waitSec float64
	for c := range ch {
		if c.Kind == ChunkRateLimit && c.RateLimit != nil {
			waitSec = c.RateLimit.WaitSec
		}
	}

	if waitSec < 0.025 || waitSec > 0.035 {
		t.Fatalf("expected waitSec ~0.03 from header, got %f", waitSec)
	}
}
