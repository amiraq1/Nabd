package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// TestTPMLimitNoticeEmitted: a provider 413 with the real Groq body shape
// must produce exactly one Notice carrying both Limit and Requested, emit
// them as event fields, end the round with exactly one terminal marker, and
// leave the session usable for a next request — the 413 is transient, not
// final.
func TestTPMLimitNoticeEmitted(t *testing.T) {
	// Real body shape from session 20260901-133251.jsonl.
	groqBody := "Request too large for model `openai/gpt-oss-20b` in organization `org_x` service tier `on_demand` on tokens per minute (TPM): Limit 8000, Requested 8968, please reduce your message size and try again."

	// The provider answers the first request with 413, then works normally.
	var muCalls sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		muCalls.Lock()
		calls++
		first := calls == 1
		muCalls.Unlock()
		if first {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge) // 413 — what Groq sends
			w.Write([]byte(`{"error":{"message":"` + groqBody + `"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"جواب"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}

	var mu sync.Mutex
	var notices []agent.Event
	var all []agent.Event
	l := &agent.Loop{
		Provider: prov,
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
		Sink: sinkFn3(func(e agent.Event) error {
			mu.Lock()
			defer mu.Unlock()
			all = append(all, e)
			if e.Type == agent.Notice {
				notices = append(notices, e)
			}
			return nil
		}),
	}

	// Run 1: the 413 fires. It must NOT look like a silent final failure.
	if err := l.Run(context.Background(), "سؤال"); err == nil {
		t.Fatal("expected a run error after the 413")
	}

	mu.Lock()
	if len(notices) != 1 {
		mu.Unlock()
		t.Fatalf("Notice count = %d, want exactly 1", len(notices))
	}
	n := notices[0]
	if !strings.Contains(n.Text, "8000") || !strings.Contains(n.Text, "8968") {
		t.Errorf("Notice must carry Limit and Requested, got %q", n.Text)
	}
	if n.Limit != 8000 || n.Requested != 8968 {
		t.Errorf("event fields: Limit=%d Requested=%d, want 8000 and 8968", n.Limit, n.Requested)
	}
	// Exactly one terminal marker (RunError) for run 1.
	terminals := 0
	for _, e := range all {
		if e.Type == agent.RunError {
			terminals++
		}
	}
	all = nil
	notices = nil
	mu.Unlock()
	if terminals != 1 {
		t.Errorf("terminal markers after 413 = %d, want exactly 1", terminals)
	}

	// Run 2: the session must accept a next request (the 413 is transient).
	if err := l.Run(context.Background(), "متابعة"); err != nil {
		t.Fatalf("second run failed — session did not survive the 413: %v", err)
	}
}

// capTools is a tool layer that reports a read cap, standing in for
// tools.Registry. The loop asks through an optional interface, so this is the
// shape production presents.
type capTools struct {
	noTools
	cap int
}

func (c capTools) ReadCapBytes() int { return c.cap }

// TestTPMNoticeNamesTheReadCap_NBD404 drives the real loop through a real 413
// and checks the Notice names the read cap in force as well as the provider's
// limit. A bare "per-minute limit" tells a reader a ceiling was hit but not
// which knob produced the request that hit it.
//
// The provider body below is the CAPTURED one, from
// ~/.ag/sessions/20260901-133251.jsonl line 8 (the same session NOTES.md cites
// for Requested 8968). Only the organization id and the upgrade link are
// dropped — they are account noise, not part of the shape being tested.
func TestTPMNoticeNamesTheReadCap_NBD404(t *testing.T) {
	groqBody := "Request too large for model `qwen/qwen3.8-27b` on tokens per minute (TPM): Limit 8000, Requested 8968, please reduce your message size and try again."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		w.Write([]byte(`{"error":{"message":"` + groqBody + `"}}`))
	}))
	defer srv.Close()

	prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}

	tools := capTools{cap: 3072}
	var mu sync.Mutex
	var notices []agent.Event
	l := &agent.Loop{
		Provider: prov,
		Tools:    tools,
		Budget:   agent.NewBudget(),
		Gate:     tools,
		Human:    tools,
		MaxTurns: 1,
		Sink: sinkFn3(func(e agent.Event) error {
			mu.Lock()
			defer mu.Unlock()
			if e.Type == agent.Notice {
				notices = append(notices, e)
			}
			return nil
		}),
	}

	if err := l.Run(context.Background(), "سؤال"); err == nil {
		t.Fatal("expected a run error after the 413")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(notices) == 0 {
		t.Fatal("no Notice emitted for the 413")
	}
	got := notices[0].Text
	if !strings.Contains(got, "read cap 3072 bytes") {
		t.Errorf("Notice does not name the read cap in force: %q", got)
	}
	if !strings.Contains(got, "8000") {
		t.Errorf("Notice lost the provider limit: %q", got)
	}
}

// TestLoopDefaultCeilingIsDefaultMaxTurns_NBD404 pins the shipped ceiling by
// exercising it: a provider that never stops asking for a tool must be cut off
// after exactly DefaultMaxTurns turns. Reading the constant alone would not
// prove the loop uses it.
func TestLoopDefaultCeilingIsDefaultMaxTurns_NBD404(t *testing.T) {
	var mu sync.Mutex
	requests := 0

	// A provider that always asks for another turn.
	forever := providerFunc(func(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
		mu.Lock()
		requests++
		mu.Unlock()
		ch := make(chan provider.Chunk, 2)
		ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{
			ID:    "call",
			Name:  "bash",
			Input: []byte(`{"cmd":"true"}`),
		}}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_use"}
		close(ch)
		return ch, nil
	})

	allow := noTools{}
	l := &agent.Loop{
		Provider: forever,
		Tools:    allow,
		Budget:   agent.NewBudget(),
		Gate:     allow,
		Human:    allow,
		Sink:     sinkFn3(func(agent.Event) error { return nil }),
		// MaxTurns left unset on purpose: the default is the subject.
	}

	err := l.Run(context.Background(), "go")
	if err == nil {
		t.Fatal("a never-settling model must hit the ceiling")
	}

	mu.Lock()
	got := requests
	mu.Unlock()

	if got != agent.DefaultMaxTurns {
		t.Fatalf("the loop ran %d turns with MaxTurns unset, want DefaultMaxTurns=%d", got, agent.DefaultMaxTurns)
	}
	if agent.DefaultMaxTurns != 40 {
		t.Fatalf("DefaultMaxTurns = %d, want the shipped 40 (NBD-404)", agent.DefaultMaxTurns)
	}
}

// providerFunc adapts a function to provider.Provider.
type providerFunc func(context.Context, provider.Request) (<-chan provider.Chunk, error)

func (f providerFunc) Name() string { return "forever" }
func (f providerFunc) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	return f(ctx, req)
}
