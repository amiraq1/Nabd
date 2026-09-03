package agent_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// fakeSSEServer speaks the OpenAI SSE dialect with a controllable pacing:
// one delta immediately, then one every 300ms. This makes a mid-stream
// interrupt deterministic — the whole point of Band 2's rework (a real
// provider streams faster than any external tool can interrupt).
func fakeSSEServer(deltaEvery time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, _ := w.(http.Flusher)

		// Answer with a long stream: 30 deltas, one every deltaEvery.
		for i := 0; i < 30; i++ {
			payload := fmt.Sprintf(
				`data: {"choices":[{"delta":{"content":"كلمة %d "},"finish_reason":""}]}`+"\n\n", i)
			if _, err := fmt.Fprint(w, payload); err != nil {
				return
			}
			fl.Flush()
			time.Sleep(deltaEvery)
		}
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
}

// slowTools is a Tools that never gets called (the fake provider streams
// text only), but satisfies the interface.
type slowTools struct{}

func (slowTools) Specs() []provider.ToolSpec { return nil }
func (slowTools) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	return "", false, fmt.Errorf("no tools in this test")
}
func (slowTools) Check(tool string) (agent.Verdict, string) { return agent.VerdictDeny, "no" }
func (slowTools) Record(tool string, d agent.Decision)      {}
func (slowTools) Effective(tool string, d agent.Decision) agent.Decision { return d }
func (slowTools) Ask(ctx context.Context, c agent.ToolCall) agent.Decision {
	return agent.Deny
}

// TestInterruptMidStreamDeterministic proves Band 2 with a fake SSE server:
// a cancel mid-stream must emit an Interrupted event, the deltas already
// emitted must survive in the journal, and the loop must return cleanly —
// no lost text, no dead session. This is repeatable, unlike pexpect against
// a real provider.
func TestInterruptMidStreamDeterministic(t *testing.T) {
	srv := fakeSSEServer(300 * time.Millisecond)
	defer srv.Close()

	prov := &provider.OpenAICompat{
		Key: "test-key", Model: "mock/model",
		BaseURL: srv.URL, Client: &http.Client{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var events []agent.Event
	l := &agent.Loop{
		Provider: prov,
		Tools:    slowTools{},
		Budget:   agent.NewBudget(),
		Gate:     slowTools{},
		Human:    slowTools{},
		Sink:     sinkFunc2(func(e agent.Event) error { mu.Lock(); events = append(events, e); mu.Unlock(); return nil }),
	}

	done := make(chan error, 1)
	go func() { done <- l.Run(ctx, "اكتب نصًا طويلًا") }()

	// Wait until at least 3 deltas have landed (mid-stream), then cancel.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := countType(events, agent.TextDelta)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	deltasBefore := countType(events, agent.TextDelta)
	mu.Unlock()
	if deltasBefore < 3 {
		t.Fatalf("stream did not start: only %d deltas before cancel", deltasBefore)
	}

	cancel() // this is what Ctrl+C reaches through to
	err := <-done
	if err != nil {
		t.Fatalf("Run returned error after interrupt: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	interrupted := countType(events, agent.Interrupted)
	if interrupted != 1 {
		t.Errorf("Interrupted events = %d, want exactly 1", interrupted)
	}
	deltasAfter := countType(events, agent.TextDelta)
	if deltasAfter < deltasBefore {
		t.Errorf("deltas shrank after interrupt: before=%d after=%d", deltasBefore, deltasAfter)
	}
	// The journal must end with the Interrupted event, not mid-delta.
	last := events[len(events)-1]
	if last.Type != agent.Interrupted {
		t.Errorf("last event after interrupt = %s, want Interrupted", last.Type)
	}
}

func countType(evs []agent.Event, typ agent.EventType) int {
	n := 0
	for _, e := range evs {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// TestFakeSSEParses ensures the fake server's dialect is accepted by the
// real OpenAI-compat reader (a broken fixture would make the test above
// pass vacuously).
func TestFakeSSEParses(t *testing.T) {
	srv := fakeSSEServer(time.Millisecond)
	defer srv.Close()

	prov := &provider.OpenAICompat{
		Key: "test-key", Model: "mock/model",
		BaseURL: srv.URL, Client: &http.Client{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := prov.Stream(ctx, provider.Request{
		Messages: []provider.Message{{Role: provider.User, Text: "مرحبا"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	n := 0
	for c := range ch {
		if c.Text != "" {
			text.WriteString(c.Text)
			n++
		}
		if c.Stop != "" {
			break
		}
	}
	if n < 10 {
		t.Fatalf("fake server delivered only %d deltas", n)
	}
	if !strings.Contains(text.String(), "كلمة 0") {
		t.Errorf("fake stream missing expected text: %q", text.String())
	}
}
