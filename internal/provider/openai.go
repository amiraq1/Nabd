package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"nabd/internal/config"
)

// OpenAICompat speaks the OpenAI chat-completions dialect, which NVIDIA
// NIM, together with most self-hosted servers, also speaks. Writing this
// against the same Provider interface is what proves the interface was
// worth having: the loop and the journal are untouched.
type OpenAICompat struct {
	Key     string
	Model   string
	BaseURL string
	Label   string
	Client  *http.Client
}

const nvidiaBase = "https://integrate.api.nvidia.com/v1"

// NewNVIDIA reads NVIDIA_API_KEY. Pick a model that actually supports
// tool calling -- most NIM models do not, and one that does not will
// happily describe the tool it would have called instead of calling it.
func NewNVIDIA() (*OpenAICompat, error) {
	k := config.Get("NVIDIA_API_KEY")
	if k == "" {
		return nil, errors.New("NVIDIA_API_KEY is not set (env or ~/.ag/config)")
	}
	m := config.GetOr("NABD_MODEL", "qwen/qwen3-coder-480b-a35b-instruct")
	base := config.GetOr("NABD_BASE_URL", nvidiaBase)
	return &OpenAICompat{
		Key: k, Model: m, BaseURL: base, Label: "nvidia",
		Client: &http.Client{},
	}, nil
}

func NewOpenRouter() (*OpenAICompat, error) {
	k := config.Get("OPENROUTER_API_KEY")
	if k == "" {
		return nil, errors.New("OPENROUTER_API_KEY غير مضبوط (في البيئة أو ~/.ag/config)")
	}
	return &OpenAICompat{
		Key:     k,
		Label:   "openrouter",
		Model:   config.GetOr("NABD_MODEL", "anthropic/claude-3.5-haiku"),
		BaseURL: config.GetOr("NABD_BASE_URL", "https://openrouter.ai/api/v1"),
		Client:  &http.Client{},
	}, nil
}

func (o *OpenAICompat) Name() string { return o.Label + "/" + shortModel(o.Model) }

func shortModel(m string) string {
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		return m[i+1:]
	}
	return m
}

func (o *OpenAICompat) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body, err := o.encode(req)
	if err != nil {
		return nil, err
	}
	out := make(chan Chunk, 32)
	go o.run(ctx, body, out)
	return out, nil
}

// run mirrors the Anthropic retry policy exactly: silent while nothing
// has been forwarded, final afterwards.
func (o *OpenAICompat) run(ctx context.Context, body []byte, out chan<- Chunk) {
	defer close(out)

	var sent atomic.Bool
	for attempt := 1; ; attempt++ {
		// A first-byte deadline is safe precisely while sent is false: nothing
		// reached the reader, so cancelling and retrying cannot duplicate text.
		// Once the first chunk lands, the stream may idle as long as it likes.
		firstByte := 25 * time.Second
		actx, cancel := context.WithCancel(ctx)
		go func() {
			t := time.NewTimer(firstByte)
			defer t.Stop()
			select {
			case <-t.C:
				if !sent.Load() {
					cancel() // silent retry, then a clear message
				}
			case <-actx.Done():
			}
		}()

		retryAfter, err := o.attempt(actx, body, out, &sent)
		cancel()

		if err == nil {
			return
		}
		if ctx.Err() != nil {
			out <- Chunk{Kind: ChunkError, Err: ctx.Err()}
			return
		}
		if sent.Load() || attempt >= maxAttempts || !transient(err) {
			if attempt >= maxAttempts && !sent.Load() && errors.Is(err, context.Canceled) {
				err = fmt.Errorf("الخادم لم يبدأ الردّ خلال 25 ثانية — جرّب NABD_MODEL آخر")
			}
			out <- Chunk{Kind: ChunkError, Err: err, Retryable: !sent.Load()}
			return
		}
		select {
		case <-ctx.Done():
			out <- Chunk{Kind: ChunkError, Err: ctx.Err()}
			return
		case <-time.After(backoff(attempt, retryAfter)):
		}
	}
}

func (o *OpenAICompat) attempt(ctx context.Context, body []byte, out chan<- Chunk, sent *atomic.Bool) (time.Duration, error) {
	hr, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(o.BaseURL, "/")+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	hr.Header.Set("authorization", "Bearer "+o.Key)
	hr.Header.Set("content-type", "application/json")
	hr.Header.Set("accept", "text/event-stream")

	resp, err := o.Client.Do(hr)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			return 0, fmt.Errorf("الموديل %q غير متاح على هذا الخادم (%d).\nاعرض المتاح:\n  curl -s %s/models -H \"Authorization: Bearer $NVIDIA_API_KEY\" | jq -r '.data[].id'\nثم: export NABD_MODEL=<id>", o.Model, resp.StatusCode, o.BaseURL)
		}
		return parseRetryAfter(resp.Header.Get("retry-after")),
			&httpError{Status: resp.StatusCode, Body: apiMessage(msg)}
	}
	return o.readSSE(ctx, resp.Body, out, sent)
}

// readSSE accumulates tool calls by index. Unlike Anthropic there is no
// block-stop event, so a call is only complete when the stream says the
// turn finished -- which is why every call is emitted at the end.
func (o *OpenAICompat) readSSE(ctx context.Context, r io.Reader, out chan<- Chunk, sent *atomic.Bool) (time.Duration, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		stop    string
		pending = map[int]*pendingCall{}
		order   []int
	)

	emit := func(c Chunk) bool {
		select {
		case out <- c:
			sent.Store(true)
			return true
		case <-ctx.Done():
			return false
		}
	}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var ev oaiChunk
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return 0, fmt.Errorf("bad sse payload: %w", err)
		}
		if ev.Error != nil && ev.Error.Message != "" {
			return 0, errors.New(ev.Error.Message)
		}
		if len(ev.Choices) == 0 {
			continue
		}
		ch := ev.Choices[0]

		if ch.Delta.Content != "" {
			if !emit(Chunk{Kind: ChunkText, Text: ch.Delta.Content}) {
				return 0, ctx.Err()
			}
		}
		// Reasoning models stream their scratchpad separately. It is not
		// part of the answer and must not reach the journal as text.
		_ = ch.Delta.Reasoning

		for _, tc := range ch.Delta.ToolCalls {
			p := pending[tc.Index]
			if p == nil {
				p = &pendingCall{}
				pending[tc.Index] = p
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				p.id = tc.ID
			}
			if tc.Function.Name != "" {
				p.name = tc.Function.Name
			}
			p.args.WriteString(tc.Function.Arguments)
		}

		if ch.FinishReason != "" {
			stop = normaliseStop(ch.FinishReason)
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}

	for i, idx := range order {
		p := pending[idx]
		raw := json.RawMessage(strings.TrimSpace(p.args.String()))
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		if !json.Valid(raw) {
			return 0, fmt.Errorf("tool %s: truncated arguments", p.name)
		}
		if p.id == "" {
			p.id = fmt.Sprintf("call_%d", i)
		}
		if !emit(Chunk{Kind: ChunkToolCall,
			Call: &ToolCall{ID: p.id, Name: p.name, Input: raw}}) {
			return 0, ctx.Err()
		}
	}

	emit(Chunk{Kind: ChunkStop, Stop: stop})
	return 0, nil
}

// normaliseStop maps OpenAI reasons onto the vocabulary the loop already
// understands, so no switch in the loop grows a provider-specific case.
func normaliseStop(r string) string {
	switch r {
	case "tool_calls", "function_call":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "stop":
		return "end_turn"
	}
	return r
}

type oaiChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// --- request encoding ---

func (o *OpenAICompat) encode(req Request) ([]byte, error) {
	maxTok := req.MaxTok
	if maxTok <= 0 {
		maxTok = 4096
	}
	w := oaiWire{Model: o.Model, MaxTok: maxTok, Stream: true, Temperature: 0.2}

	if s := strings.TrimSpace(req.System); s != "" {
		w.Messages = append(w.Messages, oaiMsg{Role: "system", Content: req.System})
	}

	for _, m := range req.Messages {
		// Tool results are their own messages here, and they must precede
		// any user text in the same turn or the server rejects the pair.
		for _, tr := range m.ToolResults {
			body := tr.Output
			if tr.IsErr {
				body = "ERROR: " + body
			}
			if body == "" {
				body = "(no output)"
			}
			w.Messages = append(w.Messages, oaiMsg{
				Role: "tool", ToolCallID: tr.ID, Content: body,
			})
		}
		switch m.Role {
		case Assistant:
			am := oaiMsg{Role: "assistant", Content: m.Text}
			for _, tc := range m.ToolCalls {
				in := string(tc.Input)
				if in == "" {
					in = "{}"
				}
				am.ToolCalls = append(am.ToolCalls, oaiToolCall{
					ID: tc.ID, Type: "function",
					Function: oaiFn{Name: tc.Name, Arguments: in},
				})
			}
			if am.Content == "" && len(am.ToolCalls) == 0 {
				continue
			}
			w.Messages = append(w.Messages, am)
		default:
			if strings.TrimSpace(m.Text) == "" {
				continue
			}
			w.Messages = append(w.Messages, oaiMsg{Role: "user", Content: m.Text})
		}
	}
	if len(w.Messages) == 0 {
		return nil, errors.New("no messages to send")
	}

	for _, t := range req.Tools {
		params := t.Schema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		w.Tools = append(w.Tools, oaiTool{Type: "function", Function: oaiFnDef{
			Name: t.Name, Description: t.Description, Parameters: params,
		}})
	}
	return json.Marshal(w)
}

type oaiWire struct {
	Model       string    `json:"model"`
	MaxTok      int       `json:"max_tokens"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature"`
	Messages    []oaiMsg  `json:"messages"`
	Tools       []oaiTool `json:"tools,omitempty"`
}

type oaiMsg struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function oaiFn  `json:"function"`
}

type oaiFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string   `json:"type"`
	Function oaiFnDef `json:"function"`
}

type oaiFnDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
