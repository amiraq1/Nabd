// Package provider talks to a model. It knows nothing about tools,
// journals, or the terminal: it turns messages into a stream of chunks.
package provider

import (
	"context"
	"encoding/json"
	"strconv"

	"nabd/internal/config"
)

type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// Message is one turn. ToolResults carries the outcome of the calls the
// previous assistant message asked for; the API requires them in the
// user turn that follows, in the same order.
type Message struct {
	Role        Role
	Text        string
	ToolCalls   []ToolCall   // assistant asked for these
	ToolResults []ToolResult // user answers with these
}

type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type ToolResult struct {
	ID     string
	Output string
	IsErr  bool
}

// ToolSpec is what the model is told a tool can do.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"input_schema"`
}

type ChunkKind uint8

const (
	ChunkText ChunkKind = iota
	ChunkToolCall
	ChunkStop
	ChunkError
	ChunkRateLimit
)

// RateLimitInfo describes an HTTP 429 rate limit encounter and the wait
// before retrying. RetryAfter carries the provider-declared wait (seconds,
// float64, source precision preserved) and RawMessage the exact response body,
// so the loop/measurements never lose what the provider said.
type RateLimitInfo struct {
	Code      int
	Limit     int
	Used      int
	Requested int
	WaitSec   float64
	Attempt   int
	Err       string
	// RetryAfter is the raw wait the provider asked for; WaitSec already
	// equals it, but keeping both separate guarantees float precision is
	// never rounded when stored. RawMessage is the un-shortened body.
	// RawRetryAfter is the verbatim Retry-After header or body text.
	RetryAfter    float64
	RawMessage    string
	RawRetryAfter string
}

// Chunk is one thing that happened during a turn.
//
// Retryable marks an error that occurred before any content reached us:
// the caller may start the turn over with nothing lost. An error after
// the first chunk is never retryable, because retrying would duplicate
// text the user already read.
type Chunk struct {
	Kind      ChunkKind
	Text      string
	Call      *ToolCall
	Stop      string // end_turn, tool_use, max_tokens, ...
	Err       error
	Retryable bool
	RateLimit *RateLimitInfo
	// PromptTokens is the provider's measured input count for the request,
	// from usage.prompt_tokens. Zero when the provider does not report it.
	// Carried on the final chunk of a turn.
	PromptTokens int
	// CompletionTokens is the provider's measured output count for the
	// request, from usage.completion_tokens. Zero when the provider does
	// not report it OR when the value is genuinely zero; use the
	// AbsentUsage flag to distinguish.
	CompletionTokens int
	// FinishReason is the raw stop reason from the provider (e.g. "length",
	// "stop", "tool_calls"). Empty when the provider does not report it.
	FinishReason string
	// AbsentUsage distinguishes "provider reported 0 tokens" from
	// "provider did not report usage at all".
	AbsentUsage bool
	// EncodedBytes is the size of the JSON request body actually sent.
	// Carried on the final chunk of a turn, for budget calibration.
	EncodedBytes int
}

type Request struct {
	System   string
	Messages []Message
	Tools    []ToolSpec
	MaxTok   int
}

// Provider streams one assistant turn. The channel is closed exactly
// once, and the implementation must respect ctx cancellation promptly.
type Provider interface {
	Name() string
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

// DefaultMaxTokens is the per-reply output cap sent when a Request does not
// set one. Providers that meter tokens-per-minute (Groq: 8000 on the free
// key) count max_tokens against the budget *before* generation, so a large
// default starves file reads. The loop always sets Request.MaxTok from
// agent.maxOutputTokens(); this fallback mirrors that resolution (same
// variable, same default, same bounds) so a bare Request behaves the same.
func DefaultMaxTokens() int {
	if v := config.Get("NABD_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 128 && n <= 8192 {
			return n
		}
	}
	return 1024
}
