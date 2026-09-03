package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestHTTP429ReportsAndReturns asserts that an HTTP 429 response is reported
// as a typed ChunkRateLimit with the exact counts and raw fields, and the
// provider returns (does NOT sleep or retry internally). The agent owns the
// retry decision.
func TestHTTP429ReportsAndReturns(t *testing.T) {
	var (
		mu       sync.Mutex
		requests [][]byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached for model qwen/qwen3.8-27b on tokens per minute (TPM): Limit 8000, Used 1859, Requested 6778. Please try again in 0.05s."}}`))
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
		Messages: []Message{{Role: User, Text: "test 429 report"}},
		MaxTok:   1024,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var (
		rateLimitChunks []*RateLimitInfo
		completed       bool
	)

	for c := range ch {
		switch c.Kind {
		case ChunkRateLimit:
			rateLimitChunks = append(rateLimitChunks, c.RateLimit)
		case ChunkStop:
			completed = true
		case ChunkError:
			t.Fatalf("unexpected ChunkError: %v", c.Err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// Provider made exactly one request and returned (no internal retry).
	if len(requests) != 1 {
		t.Fatalf("expected exactly 1 request (provider returns after 429), got %d", len(requests))
	}

	// One RateLimit chunk with the exact counts.
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
	_ = completed
}

// TestHTTP429RetryAfterHeaderParsing asserts that a Retry-After header is
// parsed (supporting floating-point seconds) and reported verbatim.
func TestHTTP429RetryAfterHeaderParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0.03")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached. Please try again in 100s."}}`))
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

	var (
		waitSec  float64
		rawRetry string
		gotLimit bool
	)
	for c := range ch {
		if c.Kind == ChunkRateLimit && c.RateLimit != nil {
			waitSec = c.RateLimit.WaitSec
			rawRetry = c.RateLimit.RawRetryAfter
			gotLimit = true
		}
	}

	if !gotLimit {
		t.Fatal("expected ChunkRateLimit")
	}
	// Header value 0.03s should be parsed.
	if waitSec < 0.025 || waitSec > 0.035 {
		t.Fatalf("expected waitSec ~0.03 from header, got %f", waitSec)
	}
	// Raw header should be preserved verbatim.
	if rawRetry != "0.03" {
		t.Errorf("expected raw Retry-After \"0.03\", got %q", rawRetry)
	}
}

// futureDate/pastDate produce RFC 7231 HTTP-date strings for testing.
func futureDate(now time.Time, secs int) string {
	return now.Add(time.Duration(secs) * time.Second).UTC().Format(http.TimeFormat)
}

func pastDate(now time.Time, secs int) string {
	return now.Add(-time.Duration(secs) * time.Second).UTC().Format(http.TimeFormat)
}

// TestParseRetryAfterVariants exercises the Retry-After parser across numeric
// (int and float), HTTP-date, and malformed inputs.
func TestParseRetryAfterVariants(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		header  string
		wantDur time.Duration
		wantRaw string
		wantOK  bool
	}{
		{"integer", "45", 45 * time.Second, "45", true},
		{"float", "4.7775", time.Duration(4.7775 * float64(time.Second)), "4.7775", true},
		{"http-date future", futureDate(now, 30), 30 * time.Second, "", true},
		{"http-date past", pastDate(now, 10), 0, "", true},
		{"malformed", "not-a-number", 0, "not-a-number", false},
		{"empty", "", 0, "", false},
		{"negative", "-5", 0, "-5", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDur, _, raw, parsed := parseWaitDuration(tc.header, "", now)
			if parsed != tc.wantOK {
				t.Errorf("parsed=%v want %v", parsed, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if tc.name == "http-date future" {
				// Allow small scheduling slack.
				if gotDur < 29*time.Second || gotDur > 31*time.Second {
					t.Errorf("got %v, want ~30s", gotDur)
				}
			} else if gotDur != tc.wantDur {
				t.Errorf("got %v, want %v", gotDur, tc.wantDur)
			}
			if tc.wantRaw != "" && raw != tc.wantRaw {
				t.Errorf("raw=%q want %q", raw, tc.wantRaw)
			}
		})
	}
}

// TestOpenAIStreamRequiresTerminalMarker verifies that a stream ending without
// [DONE] and without a finish_reason is treated as an error (truncated),
// not a silent success.
func TestOpenAIStreamRequiresTerminalMarker(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "normal with [DONE]",
			body:    "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n",
			wantErr: false,
		},
		{
			name:    "normal with finish_reason",
			body:    "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
			wantErr: false,
		},
		{
			name:    "truncated: no [DONE], no finish_reason",
			body:    "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
			wantErr: true,
		},
		{
			name:    "truncated after text",
			body:    "data: {\"choices\":[{\"delta\":{\"content\":\"some text here\"}}]}\n\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			prov := &OpenAICompat{
				BaseURL: server.URL,
				Key:     "test-key",
				Model:   "test-model",
				Client:  server.Client(),
			}

			ch, err := prov.Stream(context.Background(), Request{
				Messages: []Message{{Role: User, Text: "test"}},
				MaxTok:   1024,
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}

			var sawError bool
			for c := range ch {
				if c.Kind == ChunkError {
					sawError = true
				}
			}
			if sawError != tc.wantErr {
				t.Errorf("sawError=%v, want %v", sawError, tc.wantErr)
			}
		})
	}
}
