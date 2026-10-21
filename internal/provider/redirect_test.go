package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestP01_ProviderRefusesRedirectAndRedactsError tests Anthropic and OpenAI
// providers when an endpoint responds with HTTP 302 redirect.
func TestP01_ProviderRefusesRedirectAndRedactsError(t *testing.T) {
	t.Setenv("NABD_ENDPOINT_POLICY", "open")

	var targetHit atomic.Int32

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()

	redirectingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+"/evil", http.StatusFound)
	}))
	defer redirectingServer.Close()

	const secretKey = "sk-canary-secret-provider-key-12345"

	req := Request{
		Messages: []Message{{Role: User, Text: "hi"}},
		MaxTok:   64,
	}

	// 1. Anthropic test
	t.Run("Anthropic", func(t *testing.T) {
		a, err := NewAnthropicDialect("anthropic", redirectingServer.URL, "claude-3-5-sonnet", secretKey, 0)
		if err != nil {
			t.Fatalf("NewAnthropicDialect: %v", err)
		}

		ch, err := a.Stream(context.Background(), req)
		if err != nil {
			if strings.Contains(err.Error(), secretKey) {
				t.Fatalf("Stream error contains secret key: %v", err)
			}
		} else {
			var lastErr error
			for c := range ch {
				if c.Kind == ChunkError {
					lastErr = c.Err
				}
			}
			if lastErr == nil {
				t.Fatal("expected error on 302 response, got nil")
			}
			if strings.Contains(lastErr.Error(), secretKey) {
				t.Fatalf("error message contains secret key: %v", lastErr)
			}
		}

		// Ensure target server was not reached
		if targetHit.Load() != 0 {
			t.Fatalf("target server received request via Anthropic client redirect!")
		}
	})

	// 2. OpenAICompat test
	t.Run("OpenAICompat", func(t *testing.T) {
		targetHit.Store(0)
		o, err := NewOpenAIDialect("custom", redirectingServer.URL, "gpt-4o", secretKey, 0)
		if err != nil {
			t.Fatalf("NewOpenAIDialect: %v", err)
		}

		ch, err := o.Stream(context.Background(), req)
		if err != nil {
			if strings.Contains(err.Error(), secretKey) {
				t.Fatalf("Stream error contains secret key: %v", err)
			}
		} else {
			var lastErr error
			for c := range ch {
				if c.Kind == ChunkError {
					lastErr = c.Err
				}
			}
			if lastErr == nil {
				t.Fatal("expected error on 302 response, got nil")
			}
			if strings.Contains(lastErr.Error(), secretKey) {
				t.Fatalf("error message contains secret key: %v", lastErr)
			}
		}

		// Ensure target server was not reached
		if targetHit.Load() != 0 {
			t.Fatalf("target server received request via OpenAI client redirect!")
		}
	})
}
