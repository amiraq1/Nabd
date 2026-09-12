// Package payload measures and budgets the fixed per-request input nabd sends
// before any conversation history: the system prompt, the tool schemas, the
// model name, and the wire framing that carries them.
//
// It exists because that number is multiplied by the number of turns in a run,
// so it is a cost term and not a constant of nature. NBD-402 measured it once
// and left the measurement in a _test file in cmd/ag with a copy of the figure
// in internal/tools; this package is the single source both callers read, and
// payload_single_source_test.go fails the build if a second definition appears
// in non-test source.
//
// Everything here is deterministic and offline: no network, no provider keys.
// The measurement path uses each provider's own encoder through
// provider.Encoder, so the bytes counted are the bytes that would be sent.
package payload

import (
	"fmt"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// DefaultSystemPrompt is the model-facing contract nabd sends with every
// request. It lives here, not in cmd/ag, because its size is part of the fixed
// payload this package budgets: a prompt grown elsewhere would be a cost grown
// outside the budget.
const DefaultSystemPrompt = `You are nabd, a coding agent working inside a phone terminal 50 columns wide.
Reply in Arabic. Be extremely brief: never repeat the question, never apologise, and never list anything without cause. Two lines suffice when two suffice.`

// FormatAnthropic and FormatOpenAI are the wire formats this package can
// measure. They are the two encoders nabd ships; a third format would need a
// measurement here before its cost could be budgeted.
const (
	FormatAnthropic = "anthropic"
	FormatOpenAI    = "openai-compatible"
)

// measureModel is the model string the package measures with. Its own cost is
// reported as the Model component and is deliberately not part of the
// code-owned budget: the model comes from configuration, not from code.
const measureModel = "payload-measure"

// probeText stands in for the user turn. Its cost is counted out of Framing so
// Framing is envelope cost and not content cost.
const probeText = "x"

// Formats returns the wire formats Measure understands.
func Formats() []string { return []string{FormatAnthropic, FormatOpenAI} }

// Measurement is the decomposition of one request's fixed payload, in the
// project's estimator units (agent.EstimateText over the encoded bytes).
//
// Each component is one field's cost, isolated by removing that field and
// subtracting; the marshaller writes independent JSON fields, so the
// difference a field's removal makes is that field's cost.
type Measurement struct {
	Format  string
	System  int // the system prompt
	Schema  int // the tool specs
	Model   int // the model name (configuration, not code)
	Framing int // the message and role envelope
	Residue int // outer braces and key punctuation: belongs to no single field
	Total   int // E of the whole encoding
}

// CodeOwned is the part of the fixed payload the code grows: everything except
// the model name. This is what CodeBudgetTokens bounds.
func (m Measurement) CodeOwned() int { return m.System + m.Schema + m.Framing + m.Residue }

// Measure decomposes the fixed payload for one wire format, using the given
// system prompt and tool specs. The model is this package's placeholder; use
// CodeOwned for the code-owned total, which is model-independent by
// construction because the model's cost cancels out of every subtraction.
func Measure(format, system string, specs []provider.ToolSpec) (Measurement, error) {
	// Encode one variant: a system prompt, a tool set, and a model name.
	encode := func(model, sys string, tools []provider.ToolSpec) (int, error) {
		enc, err := encoder(format, model)
		if err != nil {
			return 0, err
		}
		b, err := enc.Encode(provider.Request{
			System:   sys,
			Messages: []provider.Message{{Role: provider.User, Text: probeText}},
			Tools:    tools,
			MaxTok:   provider.DefaultMaxTokens(),
		})
		if err != nil {
			return 0, fmt.Errorf("payload: encode %s: %w", format, err)
		}
		return agent.EstimateText(string(b)), nil
	}

	full, err := encode(measureModel, system, specs)
	if err != nil {
		return Measurement{}, err
	}
	noSystem, err := encode(measureModel, "", specs)
	if err != nil {
		return Measurement{}, err
	}
	noTools, err := encode(measureModel, system, nil)
	if err != nil {
		return Measurement{}, err
	}
	// Empty model: the encoders emit the field with an empty value rather than
	// omitting it, so the difference isolates the model string's own cost.
	noModel, err := encode("", "", nil)
	if err != nil {
		return Measurement{}, err
	}
	bareModel, err := encode(measureModel, "", nil)
	if err != nil {
		return Measurement{}, err
	}

	m := Measurement{
		Format:  format,
		System:  full - noSystem,
		Schema:  full - noTools,
		Model:   bareModel - noModel,
		Framing: noModel - agent.EstimateText(probeText),
		Total:   full,
	}
	m.Residue = m.Total - (m.System + m.Schema + m.Model + m.Framing)
	if m.System < 0 || m.Schema < 0 || m.Model < 0 || m.Framing < 0 {
		return Measurement{}, fmt.Errorf(
			"payload: %s produced a negative component (system=%d schema=%d model=%d framing=%d)",
			format, m.System, m.Schema, m.Model, m.Framing)
	}
	return m, nil
}

// Encode renders one request for a wire format through the same encoder this
// package measures with, and without sending it. The model is a parameter so a
// caller holding a captured request body can reproduce its own request
// byte-for-byte, which is how cmd/ag proves the measurement path is the wire
// path rather than a lookalike.
func Encode(format, model string, req provider.Request) ([]byte, error) {
	enc, err := encoder(format, model)
	if err != nil {
		return nil, err
	}
	return enc.Encode(req)
}

// encoder builds the provider for a wire format and model.
//
// The structs are constructed directly rather than through the route
// constructors because those reject an empty model and this package needs one
// to isolate the model's cost. Only Encode is ever called: the constructors'
// validation and retry policy do not affect the encoded bytes, and a provider
// built here is never dialled.
func encoder(format, model string) (provider.Encoder, error) {
	switch format {
	case FormatAnthropic:
		return &provider.Anthropic{Model: model}, nil
	case FormatOpenAI:
		return &provider.OpenAICompat{Model: model, BaseURL: "http://payload.invalid"}, nil
	}
	return nil, fmt.Errorf("payload: unknown wire format %q (want one of %v)", format, Formats())
}
