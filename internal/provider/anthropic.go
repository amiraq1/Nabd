package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	apiURL       = "https://api.anthropic.com/v1/messages"
	apiVersion   = "2023-06-01"
	defaultModel = "claude-sonnet-5"
	maxAttempts  = 4
	minBackoff   = time.Second
	maxBackoff   = 10 * time.Second
)

type Anthropic struct {
	Key    string
	Model  string
	Client *http.Client
}

// NewAnthropic reads the key from the environment. The key is never
// stored in the repo, never logged, and never written to the journal.
func NewAnthropic() (*Anthropic, error) {
	k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if k == "" {
		return nil, errors.New("ANTHROPIC_API_KEY is not set")
	}
	m := os.Getenv("NABD_MODEL")
	if m == "" {
		m = defaultModel
	}
	// No overall client timeout: a long turn is not a hung turn. The
	// deadline belongs to ctx, which ctrl+c cancels.
	return &Anthropic{Key: k, Model: m, Client: &http.Client{}}, nil
}

func (a *Anthropic) Name() string { return "anthropic/" + a.Model }

func (a *Anthropic) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body, err := a.encode(req)
	if err != nil {
		return nil, err
	}
	out := make(chan Chunk, 32)
	go a.run(ctx, body, out)
	return out, nil
}

// run owns the retry loop. It retries only while nothing has been sent
// downstream; once a chunk is out, a failure is final.
func (a *Anthropic) run(ctx context.Context, body []byte, out chan<- Chunk) {
	defer close(out)

	for attempt := 1; ; attempt++ {
		sent, retryAfter, err := a.attempt(ctx, body, out)
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			out <- Chunk{Kind: ChunkError, Err: ctx.Err()}
			return
		}
		if sent || attempt >= maxAttempts || !transient(err) {
			out <- Chunk{Kind: ChunkError, Err: err, Retryable: !sent}
			return
		}
		wait := backoff(attempt, retryAfter)
		select {
		case <-ctx.Done():
			out <- Chunk{Kind: ChunkError, Err: ctx.Err()}
			return
		case <-time.After(wait):
		}
	}
}

// attempt performs one request. sent reports whether any chunk was
// forwarded, which is what makes the failure unrecoverable.
func (a *Anthropic) attempt(ctx context.Context, body []byte, out chan<- Chunk) (sent bool, retryAfter time.Duration, err error) {
	hr, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return false, 0, err
	}
	hr.Header.Set("x-api-key", a.Key)
	hr.Header.Set("anthropic-version", apiVersion)
	hr.Header.Set("content-type", "application/json")
	hr.Header.Set("accept", "text/event-stream")

	resp, err := a.Client.Do(hr)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			return false, 0, fmt.Errorf("الموديل %q غير متاح على هذا الخادم (%d).", a.Model, resp.StatusCode)
		}
		ra := parseRetryAfter(resp.Header.Get("retry-after"))
		return false, ra, &httpError{Status: resp.StatusCode, Body: apiMessage(msg)}
	}
	return a.readSSE(ctx, resp.Body, out)
}

// readSSE walks the event stream. Tool inputs arrive as JSON fragments
// across many deltas, so a call is only emitted once its block closes.
func (a *Anthropic) readSSE(ctx context.Context, r io.Reader, out chan<- Chunk) (bool, time.Duration, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		sent    bool
		pending = map[int]*pendingCall{}
		stop    string
	)

	emit := func(c Chunk) bool {
		select {
		case out <- c:
			sent = true
			return true
		case <-ctx.Done():
			return false
		}
	}

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // event: and blank lines carry nothing we need
		}
		data := strings.TrimSpace(line[5:])
		if data == "" || data == "[DONE]" {
			continue
		}

		var ev sseEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return sent, 0, fmt.Errorf("bad sse payload: %w", err)
		}

		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				pending[ev.Index] = &pendingCall{
					id:   ev.ContentBlock.ID,
					name: ev.ContentBlock.Name,
				}
			}

		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" && !emit(Chunk{Kind: ChunkText, Text: ev.Delta.Text}) {
					return sent, 0, ctx.Err()
				}
			case "input_json_delta":
				if p := pending[ev.Index]; p != nil {
					p.args.WriteString(ev.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			p := pending[ev.Index]
			if p == nil {
				continue
			}
			delete(pending, ev.Index)
			raw := json.RawMessage(p.args.String())
			if len(raw) == 0 {
				raw = json.RawMessage("{}")
			}
			if !json.Valid(raw) {
				return sent, 0, fmt.Errorf("tool %s: truncated arguments", p.name)
			}
			c := &ToolCall{ID: p.id, Name: p.name, Input: raw}
			if !emit(Chunk{Kind: ChunkToolCall, Call: c}) {
				return sent, 0, ctx.Err()
			}

		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stop = ev.Delta.StopReason
			}

		case "message_stop":
			emit(Chunk{Kind: ChunkStop, Stop: stop})
			return sent, 0, nil

		case "error":
			m := "stream error"
			if ev.Error != nil && ev.Error.Message != "" {
				m = ev.Error.Message
			}
			return sent, 0, errors.New(m)
		}
	}
	if err := sc.Err(); err != nil {
		return sent, 0, err
	}
	// Stream ended without message_stop: the connection dropped.
	return sent, 0, io.ErrUnexpectedEOF
}

type pendingCall struct {
	id, name string
	args     strings.Builder
}

type sseEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// --- request encoding ---

func (a *Anthropic) encode(req Request) ([]byte, error) {
	maxTok := req.MaxTok
	if maxTok <= 0 {
		maxTok = 4096
	}
	w := wire{
		Model:  a.Model,
		MaxTok: maxTok,
		Stream: true,
		System: req.System,
		Tools:  req.Tools,
	}
	for _, m := range req.Messages {
		var blocks []any
		for _, tr := range m.ToolResults {
			blocks = append(blocks, map[string]any{
				"type":        "tool_result",
				"tool_use_id": tr.ID,
				"content":     tr.Output,
				"is_error":    tr.IsErr,
			})
		}
		if s := strings.TrimSpace(m.Text); s != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
		}
		for _, tc := range m.ToolCalls {
			in := tc.Input
			if len(in) == 0 {
				in = json.RawMessage("{}")
			}
			blocks = append(blocks, map[string]any{
				"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": in,
			})
		}
		if len(blocks) == 0 {
			continue // an empty turn is rejected by the API
		}
		w.Messages = append(w.Messages, wireMsg{Role: string(m.Role), Content: blocks})
	}
	if len(w.Messages) == 0 {
		return nil, errors.New("no messages to send")
	}
	return json.Marshal(w)
}

type wire struct {
	Model    string     `json:"model"`
	MaxTok   int        `json:"max_tokens"`
	Stream   bool       `json:"stream"`
	System   string     `json:"system,omitempty"`
	Messages []wireMsg  `json:"messages"`
	Tools    []ToolSpec `json:"tools,omitempty"`
}

type wireMsg struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

// --- errors and backoff ---

type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("http %d: %s", e.Status, e.Body)
	}
	return "http " + strconv.Itoa(e.Status)
}

// transient decides whether trying again could plausibly help. A 401 or
// a 400 will fail identically forever; retrying them only wastes battery.
func transient(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.Status == 408 || he.Status == 409 || he.Status == 429 || he.Status >= 500
	}
	if strings.Contains(err.Error(), "غير متاح على هذا الخادم") {
		return false
	}
	return true // network and EOF failures are worth one more try
}

func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxBackoff {
			return maxBackoff
		}
		return retryAfter
	}
	d := minBackoff << (attempt - 1)
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

func parseRetryAfter(s string) time.Duration {
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

// apiMessage pulls the human part out of an error body, or returns it raw.
func apiMessage(b []byte) string {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return strings.TrimSpace(string(b))
}
