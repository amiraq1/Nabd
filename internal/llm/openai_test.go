package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("NABD_BASE_URL", "https://example.invalid/v1")
	t.Setenv("NABD_MODEL", "test-model")
	t.Setenv("NABD_API_KEY", "test-key")
	cfg := ConfigFromEnv()
	if cfg.BaseURL != "https://example.invalid/v1" || cfg.Model != "test-model" || cfg.APIKey != "test-key" {
		t.Fatalf("ConfigFromEnv = %+v", cfg)
	}

	t.Setenv("NABD_BASE_URL", "")
	t.Setenv("NABD_MODEL", "")
	t.Setenv("NABD_API_KEY", "")
	cfg = ConfigFromEnv()
	if cfg.BaseURL != DefaultBaseURL || cfg.Model != DefaultModel || cfg.APIKey != "sk-no-key" {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestNewOpenAIClientDefaults(t *testing.T) {
	client := NewOpenAIClient(Config{})
	if client.cfg.BaseURL != DefaultBaseURL || client.cfg.Model != DefaultModel || client.cfg.APIKey != "sk-no-key" {
		t.Fatalf("defaults = %+v", client.cfg)
	}
	if client.cfg.MaxTokens != DefaultMaxTokens || client.cfg.Temperature != DefaultTemperature {
		t.Fatalf("generation defaults = %+v", client.cfg)
	}
}

func TestChatSuccess(t *testing.T) {
	var seen struct {
		Method string
		Auth   string
		Path   string
		Body   struct {
			Model       string           `json:"model"`
			Messages    []Message        `json:"messages"`
			Tools       []map[string]any `json:"tools"`
			Temperature float64          `json:"temperature"`
			MaxTokens   int              `json:"max_tokens"`
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Method = r.Method
		seen.Auth = r.Header.Get("Authorization")
		seen.Path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seen.Body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "hi",
					"tool_calls": [
						{"function": {"name": "read_file", "arguments": "{\"path\":\"a\"}"}},
						{"function": {"name": "list_dir", "arguments": "{}"}}
					]
				},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3}
		}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(Config{
		BaseURL: server.URL + "/v1",
		Model:   "test-model",
		APIKey:  "test-key",
		Timeout: 5 * time.Second,
	})
	resp, err := client.Chat(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hello"}},
		Tools:    []map[string]any{{"type": "function"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if seen.Method != http.MethodPost || seen.Path != "/v1/chat/completions" {
		t.Fatalf("request = %s %s", seen.Method, seen.Path)
	}
	if seen.Auth != "Bearer test-key" {
		t.Fatalf("auth = %q", seen.Auth)
	}
	if seen.Body.Model != "test-model" || seen.Body.MaxTokens != DefaultMaxTokens || seen.Body.Temperature != DefaultTemperature {
		t.Fatalf("payload = %+v", seen.Body)
	}
	if len(seen.Body.Messages) != 1 || len(seen.Body.Tools) != 1 {
		t.Fatalf("payload messages/tools = %+v", seen.Body)
	}

	if resp.Message.Content != "hi" || resp.Message.Role != "assistant" {
		t.Fatalf("message = %+v", resp.Message)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	ids := map[string]bool{}
	for _, tc := range resp.Message.ToolCalls {
		if tc.ID == "" || tc.Type != "function" {
			t.Fatalf("tool call not normalized: %+v", tc)
		}
		if ids[tc.ID] {
			t.Fatalf("duplicate generated call id %q", tc.ID)
		}
		ids[tc.ID] = true
	}
	if resp.FinishReason != "stop" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestChatRetriesOn429And5xx(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if attempts == 1 {
					if status == http.StatusTooManyRequests {
						w.Header().Set("Retry-After", "0")
					}
					w.WriteHeader(status)
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			}))
			defer server.Close()

			client := NewOpenAIClient(Config{BaseURL: server.URL, Model: "m", APIKey: "k", Timeout: 5 * time.Second})
			client.sleep = func(context.Context, time.Duration) error { return nil }

			resp, err := client.Chat(context.Background(), Request{})
			if err != nil {
				t.Fatal(err)
			}
			if attempts != 2 || resp.Message.Content != "ok" {
				t.Fatalf("attempts=%d resp=%+v", attempts, resp)
			}
		})
	}
}

func TestChatDoesNotRetry400(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer server.Close()

	client := NewOpenAIClient(Config{BaseURL: server.URL, Model: "m", APIKey: "k", Timeout: 5 * time.Second})
	client.sleep = func(context.Context, time.Duration) error { return nil }

	_, err := client.Chat(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestChatRetriesNetworkErrors(t *testing.T) {
	transport := &countingTransport{}
	client := NewOpenAIClient(Config{BaseURL: "http://example.invalid", Model: "m", APIKey: "k", Timeout: time.Second})
	client.httpClient.Transport = transport
	client.sleep = func(context.Context, time.Duration) error { return nil }

	_, err := client.Chat(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected error")
	}
	if transport.calls != maxAttempts {
		t.Fatalf("transport calls = %d, want %d", transport.calls, maxAttempts)
	}
}

type countingTransport struct {
	calls int
}

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls++
	return nil, &url.Error{Op: "Post", URL: "http://example.invalid", Err: errors.New("boom")}
}

func TestChatContextCancellation(t *testing.T) {
	client := NewOpenAIClient(Config{BaseURL: "http://example.invalid", Model: "m", APIKey: "k"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.Chat(ctx, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestChatInvalidResponse(t *testing.T) {
	for name, body := range map[string]string{
		"invalid-json": "not json",
		"no-choices":   `{"choices":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			client := NewOpenAIClient(Config{BaseURL: server.URL, Model: "m", APIKey: "k", Timeout: 5 * time.Second})
			if _, err := client.Chat(context.Background(), Request{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("empty = %v", got)
	}
	if got := parseRetryAfter("2"); got != 2*time.Second {
		t.Errorf("seconds = %v", got)
	}
	if got := parseRetryAfter("-1"); got != 0 {
		t.Errorf("negative = %v", got)
	}
	if got := parseRetryAfter("garbage"); got != 0 {
		t.Errorf("garbage = %v", got)
	}
	future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 3*time.Second {
		t.Errorf("http date = %v", got)
	}
}
