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
)

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
// key) count max_tokens against the budget *before* generation, so a 4096
// default left room for one file read per minute. 2048 is enough for a
// terminal that asks for two-line answers; NABD_MAX_TOKENS overrides it.
func DefaultMaxTokens() int {
	if v := config.Get("NABD_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 256 {
			return n
		}
	}
	return 2048
}
