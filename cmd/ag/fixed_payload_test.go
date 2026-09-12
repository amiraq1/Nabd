package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/payload"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

// NBD-402/NBD-403: the fixed per-request payload, measured on the wire and
// budgeted.
//
// The measurement itself lives in internal/payload, which is the single source
// both this guard and internal/tools read. This file's remaining job is the one
// thing only cmd/ag can do: prove that the request the real session loop puts on
// the wire is byte-identical to the request payload.Measure counts. Without
// that, the budget would guard a reconstruction rather than the real thing.
//
// Deterministic: no network (the transport never leaves the process), no
// provider keys (placeholder values), no clock dependence.
//
// Reproduce: go test ./cmd/ag -run TestFixedPayload -count=1 -v

// captureTransport records request bodies and answers with a 400. A 400 is
// non-transient (see provider.transient), so each provider makes exactly one
// attempt: the body is captured and the stream ends without a retry. The tests
// assert the count is 1 rather than assuming it.
type captureTransport struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return nil, fmt.Errorf("captureTransport: request has no body")
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, b)
	c.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"captured"}}`)),
		Request:    req,
	}, nil
}

func (c *captureTransport) captured() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.bodies))
	copy(out, c.bodies)
	return out
}

// captureModelAnthropic and captureModelOpenAI are the model strings the
// capture providers are built with. They are deliberately NOT payload's
// placeholder: reproducing the loop's request byte-for-byte is the point.
const (
	captureModelAnthropic = "claude-test"
	captureModelOpenAI    = "test-model"
)

func captureModel(format string) string {
	if format == payload.FormatAnthropic {
		return captureModelAnthropic
	}
	return captureModelOpenAI
}

// captureProvider builds a real provider of the given wire format whose
// transport records instead of dialling. The route constructors are used
// because they set RetrySingleAttempt: one attempt, no retry to inflate the
// capture.
func captureProvider(t *testing.T, format string, tr *captureTransport) provider.Provider {
	t.Helper()
	switch format {
	case payload.FormatAnthropic:
		p, err := provider.NewAnthropicForRoute(captureModelAnthropic, "test-key-not-used")
		if err != nil {
			t.Fatal(err)
		}
		p.Client = &http.Client{Transport: tr}
		return p
	case payload.FormatOpenAI:
		p, err := provider.NewOpenAICompatForRoute("groq", captureModelOpenAI, "test-key-not-used", "http://capture.invalid")
		if err != nil {
			t.Fatal(err)
		}
		p.Client = &http.Client{Transport: tr}
		return p
	}
	t.Fatalf("unknown wire format %q", format)
	return nil
}

// newToolsRegistry builds the real registry the CLI serves, rooted at a temp
// dir, so the schemas this guard measures are the shipped ones.
func newToolsRegistry(t *testing.T, dir string) *tools.Registry {
	t.Helper()
	root, err := tools.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	return tools.NewRegistry(root, sh)
}

// captureLoopWire runs the real session loop (newSessionLoop → Loop.Run →
// streamTurn) and returns the first request body it put on the wire.
func captureLoopWire(t *testing.T, format, systemText string) []byte {
	t.Helper()
	dir := t.TempDir()
	reg := newToolsRegistry(t, dir)

	tr := &captureTransport{}
	loop := newSessionLoop(captureProvider(t, format, tr), reg, gate{perm.New(reg)}, silentAsker{})
	loop.Sink = &recordSink{}
	// The system text is a parameter so the guard can be exercised with an
	// inflated prompt (the negative proof); the guard passes the real one.
	loop.System = systemText

	if err := loop.Start("fixed-payload", dir); err != nil {
		t.Fatalf("loop.Start: %v", err)
	}
	_ = loop.Run(context.Background(), "x") // a 400 ends the run; the body is captured

	bodies := tr.captured()
	if len(bodies) != 1 {
		t.Fatalf("format %s: loop sent %d requests, want exactly 1", format, len(bodies))
	}
	return bodies[0]
}

// maxTokFromWire reads max_tokens out of the captured body so a reconstruction
// cannot differ from the capture on that field alone.
func maxTokFromWire(t *testing.T, body []byte) int {
	t.Helper()
	var probe struct {
		MaxTok int `json:"max_tokens"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("parse captured body: %v", err)
	}
	if probe.MaxTok <= 0 {
		t.Fatalf("captured body carries no max_tokens: %s", body)
	}
	return probe.MaxTok
}

// TestFixedPayloadMeasurementsMatchTheWire proves payload's encoder is the wire
// encoder: rebuilding the loop's request through payload.Encode reproduces the
// captured body byte-for-byte, including the model the capture provider used.
// Without that, the budget below would guard a lookalike rather than the thing
// that is sent.
//
// payload.Measure's own Total uses the package's placeholder model, so the
// budget is stated on CodeOwned — everything but the model — which
// payload's TestCodeOwnedIsModelIndependent proves is model-independent.
func TestFixedPayloadMeasurementsMatchTheWire(t *testing.T) {
	dir := t.TempDir()
	specs := newToolsRegistry(t, dir).Specs()

	for _, format := range payload.Formats() {
		captured := captureLoopWire(t, format, payload.DefaultSystemPrompt)

		rebuilt, err := payload.Encode(format, captureModel(format), provider.Request{
			System:   payload.DefaultSystemPrompt,
			Messages: []provider.Message{{Role: provider.User, Text: "x"}},
			Tools:    specs,
			MaxTok:   maxTokFromWire(t, captured),
		})
		if err != nil {
			t.Fatalf("format %s: payload.Encode: %v", format, err)
		}
		if string(rebuilt) != string(captured) {
			t.Fatalf("format %s: payload's encoder differs from the wire body;\n captured=%s\n rebuilt =%s", format, captured, rebuilt)
		}

		m, err := payload.Measure(format, payload.DefaultSystemPrompt, specs)
		if err != nil {
			t.Fatalf("format %s: payload.Measure: %v", format, err)
		}
		wire := agent.EstimateText(string(captured))
		t.Logf("format=%s system=%d schema=%d model=%d framing=%d residue=%d code_owned=%d · wire body=%d tokens (byte-identical to payload.Encode)",
			format, m.System, m.Schema, m.Model, m.Framing, m.Residue, m.CodeOwned(), wire)

		// The wire body and the package's total differ only by the model, so
		// the difference must be non-negative and small relative to the whole.
		if diff := wire - m.CodeOwned(); diff < 0 {
			t.Errorf("format %s: the wire body (%d) is smaller than the code-owned payload (%d)", format, wire, m.CodeOwned())
		}
	}
}

// TestFixedPayloadIsWithinBudget is the guard. Every token here is multiplied
// by the number of turns in a run, so growth in this number is growth in every
// session's bill — see READ_CAP_TURN_COST in docs/TECH_DEBT.md.
func TestFixedPayloadIsWithinBudget(t *testing.T) {
	dir := t.TempDir()
	specs := newToolsRegistry(t, dir).Specs()
	for _, format := range payload.Formats() {
		assertFixedPayloadWithinBudget(t, format, payload.DefaultSystemPrompt, specs)
	}
}

// assertFixedPayloadWithinBudget measures one format's fixed payload for the
// given system text and fails if its code-owned part exceeds the code budget.
// The system text is a parameter so the guard can be exercised with an inflated
// prompt (the negative proof).
func assertFixedPayloadWithinBudget(t *testing.T, format, systemText string, specs []provider.ToolSpec) {
	t.Helper()

	m, err := payload.Measure(format, systemText, specs)
	if err != nil {
		t.Fatalf("payload.Measure: %v", err)
	}
	budget := payload.CodeBudgetTokens()
	t.Logf("format=%s system=%d schema=%d model=%d framing=%d code_owned=%d · code_budget=%d · rules_budget=%d (reserved for NBD-410)",
		format, m.System, m.Schema, m.Model, m.Framing, m.CodeOwned(), budget, payload.RulesBudgetTokens())

	if m.CodeOwned() > budget {
		t.Errorf("the code-owned fixed per-request payload for %s is %d tokens, above the code budget of %d "+
			"(system=%d schema=%d framing=%d residue=%d). Every token here is multiplied by the number of turns "+
			"in a run, so this is not a one-off cost. The budget is derived in internal/payload from named inputs; "+
			"re-derive it only with a fresh measurement, and record why in READ_CAP_TURN_COST (docs/TECH_DEBT.md).",
			format, m.CodeOwned(), budget, m.System, m.Schema, m.Framing, m.Residue)
	}
}
