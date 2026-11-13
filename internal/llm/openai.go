package llm

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxAttempts      = 4
	maxRetryAfter    = 30 * time.Second
	maxResponseBytes = 4 << 20
)

// OpenAIClient talks to an OpenAI-compatible /chat/completions endpoint.
type OpenAIClient struct {
	cfg         Config
	httpClient  *http.Client
	sleep       func(context.Context, time.Duration) error
	backoffBase time.Duration
}

func NewOpenAIClient(cfg Config) *OpenAIClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "sk-no-key"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = DefaultTemperature
	}
	return &OpenAIClient{
		cfg:         cfg,
		httpClient:  &http.Client{Timeout: cfg.Timeout},
		sleep:       sleepContext,
		backoffBase: 500 * time.Millisecond,
	}
}

func (c *OpenAIClient) Chat(ctx context.Context, req Request) (*Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	body, err := c.encode(req)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"

	var lastErr error
	var wait time.Duration
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if wait <= 0 {
				wait = c.backoff(attempt)
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
			wait = 0
		}

		response, retryable, retryAfter, err := c.attempt(ctx, endpoint, body)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !retryable {
			return nil, err
		}
		if retryAfter > maxRetryAfter {
			retryAfter = maxRetryAfter
		}
		wait = retryAfter
	}
	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

func (c *OpenAIClient) encode(req Request) ([]byte, error) {
	payload := map[string]any{
		"model":       c.cfg.Model,
		"messages":    req.Messages,
		"temperature": c.cfg.Temperature,
		"max_tokens":  c.cfg.MaxTokens,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	return body, nil
}

func (c *OpenAIClient) attempt(ctx context.Context, endpoint string, body []byte) (*Response, bool, time.Duration, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, false, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, 0, ctx.Err()
		}
		return nil, true, 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, true, 0, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		delay := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, retryable, delay, fmt.Errorf("status %d: %s", resp.StatusCode, clipText(string(data), 300))
	}

	parsed, err := parseResponse(data)
	if err != nil {
		return nil, false, 0, err
	}
	return parsed, false, 0, nil
}

func parseResponse(data []byte) (*Response, error) {
	var result struct {
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("response has no choices")
	}

	choice := result.Choices[0]
	msg := choice.Message
	if msg.Role == "" {
		msg.Role = "assistant"
	}
	for i := range msg.ToolCalls {
		if msg.ToolCalls[i].ID == "" {
			msg.ToolCalls[i].ID = newCallID(i)
		}
		if msg.ToolCalls[i].Type == "" {
			msg.ToolCalls[i].Type = "function"
		}
	}
	return &Response{Message: msg, FinishReason: choice.FinishReason, Usage: result.Usage}, nil
}

func (c *OpenAIClient) backoff(attempt int) time.Duration {
	base := c.backoffBase * time.Duration(1<<uint(attempt))
	if base <= 0 {
		base = time.Second
	}
	if base > 10*time.Second {
		base = 10 * time.Second
	}
	return time.Duration(mrand.Int63n(int64(base)))
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func newCallID(index int) string {
	buf := make([]byte, 4)
	if _, err := crand.Read(buf); err != nil {
		return fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), index+1)
	}
	return fmt.Sprintf("call_%s_%d", hex.EncodeToString(buf), index+1)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func clipText(s string, n int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
