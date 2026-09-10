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
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

// NBD-402: provenance of the fixed per-request payload.
//
// NBD-401 measured the cumulative cost of a read but took the fixed
// per-request payload (system + tool schemas + wire framing) from
// readOverhead = 2210 in internal/tools/read.go, a number whose composition
// was never established — and since that term is multiplied by the request
// count, an error in it is multiplied too.
//
// This file measures the real thing: the loop from NBD-400 (newSessionLoop)
// builds the request, and the provider's own encoder serialises it, captured
// at the transport — so the bytes are the bytes that would go on the wire, not
// a Go-structure estimate. Deterministic: no network (the transport never
// leaves the process), no provider keys (dummy values), no clock dependence.
//
// Reproduce: go test ./cmd/ag -run TestFixedPayload -count=1 -v

// captureTransport records request bodies and answers with a 400. A 400 is
// non-transient (see provider.transient), so each provider makes exactly one
// attempt: the body is captured and the stream ends without a retry. The test
// asserts the count is 1 rather than assuming it.
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

// captureProvider builds a real provider of the given wire format whose
// transport records instead of dialling. route constructors are used because
// they set RetrySingleAttempt: one attempt, no retry to inflate the capture.
func captureProvider(t *testing.T, format string, tr *captureTransport) provider.Provider {
	t.Helper()
	switch format {
	case formatAnthropic:
		p, err := provider.NewAnthropicForRoute("claude-test", "test-key-not-used")
		if err != nil {
			t.Fatal(err)
		}
		p.Client = &http.Client{Transport: tr}
		return p
	case formatOpenAI:
		p, err := provider.NewOpenAICompatForRoute("groq", "test-model", "test-key-not-used", "http://capture.invalid")
		if err != nil {
			t.Fatal(err)
		}
		p.Client = &http.Client{Transport: tr}
		return p
	}
	t.Fatalf("unknown wire format %q", format)
	return nil
}

const (
	formatAnthropic = "anthropic"
	formatOpenAI    = "openai-compatible"
)

// sendCapture streams one request and returns the exact body that reached the
// transport. The stream's outcome is irrelevant — a 400 is expected — but the
// single-attempt assertion is not: two bodies would mean a retry and an
// ambiguous measurement.
func sendCapture(t *testing.T, format string, req provider.Request) []byte {
	t.Helper()
	tr := &captureTransport{}
	p := captureProvider(t, format, tr)
	ch, err := p.Stream(context.Background(), req)
	if err == nil && ch != nil {
		for range ch {
		}
	}
	bodies := tr.captured()
	if len(bodies) != 1 {
		t.Fatalf("format %s: captured %d request bodies, want exactly 1 (a retry would make the measurement ambiguous)", format, len(bodies))
	}
	return bodies[0]
}

// fixedPayload is the decomposition of one request's fixed cost, in the
// project's own estimator units (agent.EstimateText over the wire bytes).
type fixedPayload struct {
	format string
	system int
	schema int
	frame  int
	body   int
}

// newToolsRegistry builds the real registry the CLI serves, rooted at a temp
// dir, so the measured schemas are the shipped ones.
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

// captureLoopWire runs the real session loop with the given system text and
// returns the first request body it put on the wire, plus the advertised specs.
// It is the load-bearing step of this measurement: the request is built by
// production code (newSessionLoop → Loop.Run → streamTurn), not by hand.
func captureLoopWire(t *testing.T, format, systemText string) ([]byte, []provider.ToolSpec) {
	t.Helper()
	dir := t.TempDir()
	reg := newToolsRegistry(t, dir)

	tr := &captureTransport{}
	prov := captureProvider(t, format, tr)

	loop := newSessionLoop(prov, reg, gate{perm.New(reg)}, silentAsker{})
	loop.Sink = &recordSink{}
	// The system text is supplied by the caller so the guard below can be
	// exercised with an inflated prompt; the guard itself passes `system`.
	loop.System = systemText

	if err := loop.Start("fixed-payload", dir); err != nil {
		t.Fatalf("loop.Start: %v", err)
	}
	// A 400 ends the run; the body of the first request is already captured.
	_ = loop.Run(context.Background(), "x")

	bodies := tr.captured()
	if len(bodies) != 1 {
		t.Fatalf("format %s: loop sent %d requests, want exactly 1", format, len(bodies))
	}
	return bodies[0], reg.Specs()
}

func wireTokens(b []byte) int { return agent.EstimateText(string(b)) }

// measureFixedPayload decomposes the fixed payload for one wire format:
//
//	system = E(with system)  − E(same request, no system)
//	schema = E(with specs)   − E(same request, no specs)
//	frame  = E(the rest)     − E(the message text)
//
// The subtractions are exact because marshalling writes independent JSON
// fields; the residue they leave (outer braces, key punctuation) is reported
// rather than hidden.
func measureFixedPayload(t *testing.T, format, systemText string) fixedPayload {
	t.Helper()

	wire, specs := captureLoopWire(t, format, systemText)
	const msg = "x"

	// Re-encode the same request four ways. r0 must reproduce the captured
	// bytes exactly; that equality is what proves the variants describe the
	// request the loop actually sent.
	maxTok := maxTokFromWire(t, wire)
	msgs := []provider.Message{{Role: provider.User, Text: msg}}
	r0 := provider.Request{System: systemText, Messages: msgs, Tools: specs, MaxTok: maxTok}
	r1 := provider.Request{System: "", Messages: msgs, Tools: specs, MaxTok: maxTok}
	r2 := provider.Request{System: systemText, Messages: msgs, MaxTok: maxTok}
	r3 := provider.Request{Messages: msgs, MaxTok: maxTok}

	rebuild := sendCapture(t, format, r0)
	if string(rebuild) != string(wire) {
		t.Fatalf("format %s: re-encoded request differs from the captured one;\n captured=%s\n rebuild =%s", format, wire, rebuild)
	}

	e0 := wireTokens(rebuild)
	e1 := wireTokens(sendCapture(t, format, r1))
	e2 := wireTokens(sendCapture(t, format, r2))
	e3 := wireTokens(sendCapture(t, format, r3))

	system := e0 - e1
	schema := e0 - e2
	frame := e3 - agent.EstimateText(msg)

	// Sanity: the parts may not exceed the whole they came from.
	if system < 0 || schema < 0 || frame < 0 {
		t.Fatalf("format %s: negative component (system=%d schema=%d frame=%d)", format, system, schema, frame)
	}
	return fixedPayload{format: format, system: system, schema: schema, frame: frame, body: e0}
}

// maxTokFromWire reads max_tokens out of the captured body so the rebuilt
// request cannot differ from the captured one on that field alone. Both wire
// formats name it the same way.
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

// TestFixedPayloadDecomposition reports the three named components for both
// wire formats. The framing difference is the point: the previous single
// constant behaved as if the two formats framed a request identically.
func TestFixedPayloadDecomposition(t *testing.T) {
	t.Logf("%-18s %8s %8s %8s %8s %10s", "format", "system", "schema", "frame", "sum", "wire_total")
	for _, format := range []string{formatAnthropic, formatOpenAI} {
		p := measureFixedPayload(t, format, system)
		sum := p.system + p.schema + p.frame
		t.Logf("%-18s %8d %8d %8d %8d %10d (residue %+d)",
			p.format, p.system, p.schema, p.frame, sum, p.body, p.body-sum)

		if p.system <= 0 {
			t.Errorf("format %s: system component is %d; the CLI prompt is not reaching the wire", format, p.system)
		}
		if p.schema <= 0 {
			t.Errorf("format %s: schema component is %d; the tool specs are not reaching the wire", format, p.schema)
		}
		if p.frame <= 0 {
			t.Errorf("format %s: framing component is %d; the JSON envelope is not being counted", format, p.frame)
		}
	}
}

// fixedPayloadCeiling is the ceiling for the fixed per-request payload
// (system + tool schemas + wire framing) in EITHER wire format.
//
// DERIVATION, not invention. It is computed in code from three named inputs,
// so a reviewer recomputes it instead of trusting a number:
//
//	measuredFixedPayload = the two wire figures recorded below, from
//	                       TestFixedPayloadDecomposition at the commit that
//	                       introduced this guard (anthropic 752, openai 809,
//	                       with a residue of +1/+0 tokens)
//	headroom             = the margin, stated separately so it is a decision
//	                       on the record rather than an unexplained gap
//	ceiling              = round_up_to_100(max(measured) × (1 + headroom))
//	                     = round_up_to_100(809 × 1.25) = 1100
//
// The recorded figures are inputs to the ceiling, not assertions: the guard
// measures live and compares, so a prompt or schema growth beyond the headroom
// fails the build instead of silently costing every session more.
const (
	measuredFixedPayloadAnthropic = 752
	measuredFixedPayloadOpenAI    = 809
	fixedPayloadHeadroom          = 0.25
)

// fixedPayloadCeiling returns the derived ceiling described above.
func fixedPayloadCeiling() int {
	largest := measuredFixedPayloadAnthropic
	if measuredFixedPayloadOpenAI > largest {
		largest = measuredFixedPayloadOpenAI
	}
	withHeadroom := float64(largest) * (1 + fixedPayloadHeadroom)
	// Round up to the next 100 so the ceiling reads as a budget, not as a
	// restatement of the measurement to the token.
	ceiling := int(withHeadroom/100) * 100
	if ceiling < int(withHeadroom) {
		ceiling += 100
	}
	return ceiling
}

// TestFixedPayloadBudget is the guard: the fixed payload must stay under the
// derived ceiling. Every token here is multiplied by the number of turns, so
// growth in this number is growth in every session's bill — see
// READ_CAP_TURN_COST in docs/TECH_DEBT.md.
func TestFixedPayloadBudget(t *testing.T) {
	for _, format := range []string{formatAnthropic, formatOpenAI} {
		assertFixedPayloadWithinBudget(t, format, system)
	}
}

// assertFixedPayloadWithinBudget measures one format's fixed payload for the
// given system text and fails if it exceeds the derived ceiling. The system
// text is a parameter so the guard can be exercised with an inflated prompt
// (the negative proof); production passes the real `system`.
func assertFixedPayloadWithinBudget(t *testing.T, format, systemText string) {
	t.Helper()

	p := measureFixedPayload(t, format, systemText)
	total := p.system + p.schema + p.frame
	ceiling := fixedPayloadCeiling()

	t.Logf("format=%s system=%d schema=%d framing=%d fixed_total=%d ceiling=%d (headroom %.0f%%, recorded max %d)",
		format, p.system, p.schema, p.frame, total, ceiling, fixedPayloadHeadroom*100,
		maxInt(measuredFixedPayloadAnthropic, measuredFixedPayloadOpenAI))

	if total > ceiling {
		t.Errorf("fixed per-request payload for %s is %d tokens, above the derived ceiling of %d "+
			"(system=%d schema=%d framing=%d). Every token here is multiplied by the number of turns in a run, "+
			"so this is not a one-off cost. Re-derive the ceiling only with a fresh measurement and update "+
			"READ_CAP_TURN_COST in docs/TECH_DEBT.md.",
			format, total, ceiling, p.system, p.schema, p.frame)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
