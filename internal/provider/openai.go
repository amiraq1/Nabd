package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
	Client  *http.Client
}

// TPMError is Groq's per-minute token ceiling response. It is deliberately
// distinct from retryable transport/server errors: waiting is required, but
// the provider must not issue an automatic retry.
type TPMError struct {
	Limit     int
	Requested int
	Body      string
}

func (e *TPMError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("http 413: %s", e.Body)
	}
	return "http 413"
}

func parseTPMCounts(s string) (limit, requested int) {
	value := func(label string) int {
		i := strings.Index(s, label)
		if i < 0 {
			return 0
		}
		s = s[i+len(label):]
		j := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == 0 {
			return 0
		}
		n, err := strconv.Atoi(s[:j])
		if err != nil {
			return 0
		}
		return n
	}
	return value("Limit "), value("Requested ")
}

const nvidiaBase = "https://integrate.api.nvidia.com/v1"

// nvidiaDefaultModel is a catalog entry verified live on 2026-09-03 and
// known to call tools. Defaults on NIM rot (Friction 1: the previous one
// answered 410); when this one goes, the 404/410 branch below tells the
// user how to pick another instead of guessing for them.
const nvidiaDefaultModel = "moonshotai/kimi-k2.6"

// NewNVIDIA reads NVIDIA_API_KEY. Pick a model that actually supports
// tool calling -- most NIM models do not, and one that does not will
// happily describe the tool it would have called instead of calling it.
func NewNVIDIA() (*OpenAICompat, error) {
	k := config.Get("NVIDIA_API_KEY")
	if k == "" {
		return nil, errors.New("NVIDIA_API_KEY is not set (env or ~/.ag/config)")
	}
	m := config.GetOr("NABD_MODEL", nvidiaDefaultModel)
	base := config.GetOr("NABD_BASE_URL", nvidiaBase)
	return &OpenAICompat{
		Key: k, Model: m, BaseURL: base,
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
		Model:   config.GetOr("NABD_MODEL", "anthropic/claude-3.5-haiku"),
		BaseURL: config.GetOr("NABD_BASE_URL", "https://openrouter.ai/api/v1"),
		Client:  &http.Client{},
	}, nil
}

func NewGroq() (*OpenAICompat, error) {
	k := config.Get("GROQ_API_KEY")
	if k == "" {
		return nil, errors.New("GROQ_API_KEY غير مضبوط (في البيئة أو ~/.ag/config)")
	}
	return &OpenAICompat{
		Key:     k,
		Model:   config.GetOr("NABD_MODEL", "qwen-2.5-32b"),
		BaseURL: "https://api.groq.com/openai/v1",
		Client:  &http.Client{},
	}, nil
}

func (c *OpenAICompat) Label() string {
	if u, err := url.Parse(c.BaseURL); err == nil {
		return strings.TrimPrefix(u.Hostname(), "api.")
	}
	return "unknown"
}

// Name carries the full model ID exactly as it is sent on the wire. The
// truncated form once displayed "qwen3.8-27b" with its namespace cut off,
// which read like a malformed model name — what the banner shows must be
// what was sent.
func (o *OpenAICompat) Name() string { return o.Label() + "/" + o.Model }

// keyVar names the environment variable a user of this host would set, for
// error messages that spell out a curl command. Unknown hosts get a
// neutral name rather than a wrong one.
func (o *OpenAICompat) keyVar() string {
	switch l := o.Label(); {
	case strings.Contains(l, "nvidia"):
		return "NVIDIA_API_KEY"
	case strings.Contains(l, "openrouter"):
		return "OPENROUTER_API_KEY"
	case strings.Contains(l, "groq"):
		return "GROQ_API_KEY"
	}
	return "API_KEY"
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

// RateLimitError represents an HTTP 429 response from an upstream provider.
type RateLimitError struct {
	Status    int
	Body      string
	Limit     int
	Used      int
	Requested int
	WaitSec   float64
	Raw       string // verbatim Retry-After header or body text
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Status, e.Body)
}

var (
	reBodyWait = regexp.MustCompile(`(?i)try again in\s+([0-9]+(?:\.[0-9]+)?)s?`)
	reTPMFull  = regexp.MustCompile(`Limit\s+(\d+),\s*Used\s+(\d+),\s*Requested\s+(\d+)`)
)

// parseWaitDuration extracts the wait duration from a 429 response. It accepts
// a numeric value (int or float seconds) or an RFC 7231 HTTP-date. It returns
// the parsed duration, the raw seconds (for float-precision storage), and the
// raw string (header or body text) for verbatim journaling. A zero duration
// with parsed=false signals the caller to use fallback backoff.
func parseWaitDuration(retryAfterHeader, body string, now time.Time) (time.Duration, float64, string, bool) {
	// 1. Retry-After header — may be seconds or HTTP-date.
	if s := strings.TrimSpace(retryAfterHeader); s != "" {
		if sec, err := strconv.ParseFloat(s, 64); err == nil && sec > 0 {
			return time.Duration(sec * float64(time.Second)), sec, s, true
		}
		if t, err := http.ParseTime(s); err == nil {
			d := t.Sub(now)
			if d < 0 {
				d = 0 // past date: don't wait, but report parsed
			}
			return d, d.Seconds(), s, true
		}
		return 0, 0, s, false // malformed header
	}
	// 2. Provider body text, e.g. "try again in 4.7775s"
	if m := reBodyWait.FindStringSubmatch(body); len(m) > 1 {
		raw := strings.TrimSpace(m[0])
		if sec, err := strconv.ParseFloat(m[1], 64); err == nil && sec > 0 {
			return time.Duration(sec * float64(time.Second)), sec, raw, true
		}
		return 0, 0, raw, false
	}
	return 0, 0, "", false
}

func parseTPMFullCounts(body string) (limit, used, requested int) {
	if m := reTPMFull.FindStringSubmatch(body); len(m) == 4 {
		limit, _ = strconv.Atoi(m[1])
		used, _ = strconv.Atoi(m[2])
		requested, _ = strconv.Atoi(m[3])
		return
	}
	l, r := parseTPMCounts(body)
	return l, 0, r
}

// run performs a single request and reports the result. It does NOT sleep
// or retry on 429: the agent owns the wait decision and the retry loop.
// This eliminates double-waiting (provider sleeping, then agent sleeping)
// and centralizes rate-limit policy in one place.
func (o *OpenAICompat) run(ctx context.Context, body []byte, out chan<- Chunk) {
	defer close(out)

	var sent atomic.Bool

	// One attempt. On 429, emit a typed RateLimit chunk and return the
	// parsed wait so the agent can decide how long to wait before retrying.
	firstByte := 25 * time.Second
	actx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTimer(firstByte)
		defer t.Stop()
		select {
		case <-t.C:
			if !sent.Load() {
				cancel() // first byte never arrived; fail fast
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

	var rle *RateLimitError
	if errors.As(err, &rle) {
		// Report the 429 to the agent. Do NOT sleep here — the agent owns
		// the wait. Return a sentinel error so the agent knows this was a
		// rate limit (not a fatal error) and can retry after waiting.
		out <- Chunk{
			Kind: ChunkRateLimit,
			RateLimit: &RateLimitInfo{
				Code:          rle.Status,
				Limit:         rle.Limit,
				Used:          rle.Used,
				Requested:     rle.Requested,
				WaitSec:       rle.WaitSec,
				Attempt:       1,
				Err:           err.Error(),
				RetryAfter:    rle.WaitSec,
				RawMessage:    rle.Body,
				RawRetryAfter: rle.Raw,
			},
		}
		// Close the channel; the agent sees the RateLimit chunk and the
		// stream end, then decides whether to wait and retry.
		return
	}

	// Non-429 transient error: keep the existing single-retry behavior
	// for network/EOF failures. This is not rate-limit policy.
	if !sent.Load() && transient(err) {
		select {
		case <-ctx.Done():
			out <- Chunk{Kind: ChunkError, Err: ctx.Err()}
			return
		case <-time.After(backoff(1, retryAfter)):
		}
		retryAfter, err = o.attempt(ctx, body, out, &sent)
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			out <- Chunk{Kind: ChunkError, Err: ctx.Err()}
			return
		}
	}

	if !sent.Load() && errors.Is(err, context.Canceled) {
		err = fmt.Errorf("الخادم لم يبدأ الردّ خلال 25 ثانية — جرّب NABD_MODEL آخر")
	}
	out <- Chunk{Kind: ChunkError, Err: err, Retryable: !sent.Load()}
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
			return 0, fmt.Errorf("الموديل %q غير متاح على هذا الخادم (%d).\nاعرض المتاح:\n  curl -s %s/models -H \"Authorization: Bearer $%s\" | jq -r '.data[].id'\nثم ضع NABD_MODEL=<id> في ~/.ag/config", o.Model, resp.StatusCode, o.BaseURL, o.keyVar())
		}
		// Groq reports per-minute TPM violations as http 413 with a body
		// naming "Limit N" and "Requested M". It is a rate ceiling, not a
		// permanent failure, and must not be auto-retried — the caller
		// decides (a human is told to wait).
		if resp.StatusCode == http.StatusRequestEntityTooLarge {
			body := apiMessage(msg)
			limit, requested := parseTPMCounts(body)
			if limit > 0 || requested > 0 {
				return 0, &TPMError{Limit: limit, Requested: requested, Body: body}
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			body := apiMessage(msg)
			waitDur, waitSec, raw, _ := parseWaitDuration(resp.Header.Get("retry-after"), body, time.Now())
			lim, usd, req := parseTPMFullCounts(body)
			return waitDur, &RateLimitError{
				Status:    resp.StatusCode,
				Body:      body,
				Limit:     lim,
				Used:      usd,
				Requested: req,
				WaitSec:   waitSec,
				Raw:       raw,
			}
		}
		return parseRetryAfter(resp.Header.Get("retry-after")),
			&httpError{Status: resp.StatusCode, Body: apiMessage(msg)}
	}
	return o.readSSE(ctx, resp.Body, out, sent, len(body))
}

// readSSE accumulates tool calls by index. Unlike Anthropic there is no
// block-stop event, so a call is only complete when the stream says the
// turn finished -- which is why every call is emitted at the end.
func (o *OpenAICompat) readSSE(ctx context.Context, r io.Reader, out chan<- Chunk, sent *atomic.Bool, encodedBytes int) (time.Duration, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		stop             string
		promptTokens     int
		completionTokens int
		finishReason     string
		usageReported    bool
		gotDone          bool // true when the stream terminated with [DONE]
		pending          = map[int]*pendingCall{}
		order            []int
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
			gotDone = true
			break
		}

		var ev oaiChunk
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return 0, fmt.Errorf("bad sse payload: %w", err)
		}
		if ev.Error != nil && ev.Error.Message != "" {
			return 0, errors.New(ev.Error.Message)
		}
		if ev.Usage != nil {
			usageReported = true
			if ev.Usage.PromptTokens > 0 {
				promptTokens = ev.Usage.PromptTokens
			}
			if ev.Usage.CompletionTokens > 0 {
				completionTokens = ev.Usage.CompletionTokens
			}
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
			finishReason = ch.FinishReason
			stop = normaliseStop(ch.FinishReason)
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	// A stream that ends without [DONE] and without a reliable finish_reason
	// was truncated mid-flight (connection drop). Treat it as an error so the
	// agent does not silently accept a partial answer as complete.
	if !gotDone && finishReason == "" {
		return 0, io.ErrUnexpectedEOF
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

	emit(Chunk{Kind: ChunkStop, Stop: stop, PromptTokens: promptTokens,
		CompletionTokens: completionTokens, FinishReason: finishReason,
		EncodedBytes: encodedBytes, AbsentUsage: !usageReported})
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
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// --- request encoding ---

func (o *OpenAICompat) encode(req Request) ([]byte, error) {
	maxTok := req.MaxTok
	if maxTok <= 0 {
		maxTok = DefaultMaxTokens()
	}
	w := oaiWire{Model: o.Model, MaxTok: maxTok, Stream: true, Temperature: 0.2,
		// Groq/OpenAI omit usage in a streaming response unless asked.
		// The loop's calibration reads prompt_tokens from the final chunk;
		// without include_usage the ratio is never measured.
		StreamOptions: &oaiStreamOptions{IncludeUsage: true}}

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
	Model         string            `json:"model"`
	MaxTok        int               `json:"max_tokens"`
	Stream        bool              `json:"stream"`
	Temperature   float64           `json:"temperature"`
	StreamOptions *oaiStreamOptions `json:"stream_options,omitempty"`
	Messages      []oaiMsg          `json:"messages"`
	Tools         []oaiTool         `json:"tools,omitempty"`
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
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
