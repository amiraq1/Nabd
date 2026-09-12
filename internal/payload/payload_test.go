package payload

import (
	"encoding/json"
	"testing"

	"nabd/internal/provider"
)

// probeSpecs is a small, fixed spec set used to verify the decomposition
// mechanics. It is deliberately NOT the shipped set: payload cannot import
// internal/tools (tools consumes this package), so the measurement against the
// real schemas belongs to the caller that has them — see
// TestFixedPayloadIsWithinBudget in cmd/ag, which supplies reg.Specs().
func probeSpecs() []provider.ToolSpec {
	return []provider.ToolSpec{
		{
			Name:        "read_file",
			Description: "Read a text file.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
	}
}

// TestMeasureDecomposesTheFixedPayload verifies the decomposition is
// well-formed and leaves the residue visible instead of folding it into a
// component. Both wire formats are measured: they frame a request differently,
// which a single constant could not represent.
func TestMeasureDecomposesTheFixedPayload(t *testing.T) {
	t.Logf("%-18s %8s %8s %8s %8s %8s %10s %8s", "format", "system", "schema", "model", "framing", "residue", "encoded", "code_owned")
	for _, format := range Formats() {
		m, err := Measure(format, DefaultSystemPrompt, probeSpecs())
		if err != nil {
			t.Fatalf("Measure(%s): %v", format, err)
		}
		t.Logf("%-18s %8d %8d %8d %8d %8d %10d %8d",
			m.Format, m.System, m.Schema, m.Model, m.Framing, m.Residue, m.Total, m.CodeOwned())

		if m.System <= 0 {
			t.Errorf("%s: system component is %d; the prompt is not reaching the encoder", format, m.System)
		}
		if m.Schema <= 0 {
			t.Errorf("%s: schema component is %d; the tool specs are not reaching the encoder", format, m.Schema)
		}
		if m.Model <= 0 {
			t.Errorf("%s: model component is %d; the model name is not being counted", format, m.Model)
		}
		if m.Framing <= 0 {
			t.Errorf("%s: framing component is %d; the JSON envelope is not being counted", format, m.Framing)
		}
		if m.CodeOwned() >= m.Total {
			t.Errorf("%s: code-owned total %d should be smaller than the encoded total %d, since the model is excluded",
				format, m.CodeOwned(), m.Total)
		}
	}
}

// TestCodeOwnedIsModelIndependent is the property the budget rests on: the
// code-owned total must not depend on the model name, which comes from
// configuration. The package's own placeholder would otherwise quietly set the
// budget for every user's model.
func TestCodeOwnedIsModelIndependent(t *testing.T) {
	// Measure always uses its placeholder, so independence is structural; this
	// checks the arithmetic that makes it so — the model cancels out of each
	// subtraction, leaving only the residue and the named parts.
	for _, format := range Formats() {
		m, err := Measure(format, DefaultSystemPrompt, probeSpecs())
		if err != nil {
			t.Fatal(err)
		}
		sum := m.System + m.Schema + m.Framing + m.Residue
		if m.CodeOwned() != sum {
			t.Fatalf("%s: CodeOwned=%d, want system+schema+framing+residue=%d", format, m.CodeOwned(), sum)
		}
		if m.Total-m.Model != m.CodeOwned() {
			t.Fatalf("%s: Total-model=%d, want CodeOwned=%d (the model must be the only excluded part)",
				format, m.Total-m.Model, m.CodeOwned())
		}
	}
}

// TestMeasureRespondsToItsInputs proves the components are really measured from
// the request and not constants wearing a function: a bigger prompt must cost
// more, and dropping the tools must remove the schema component.
func TestMeasureRespondsToItsInputs(t *testing.T) {
	small, err := Measure(FormatAnthropic, "short", probeSpecs())
	if err != nil {
		t.Fatal(err)
	}
	large, err := Measure(FormatAnthropic, "short"+" padding sentence repeated many times", probeSpecs())
	if err != nil {
		t.Fatal(err)
	}
	if large.System <= small.System {
		t.Errorf("a longer prompt did not cost more: %d vs %d", large.System, small.System)
	}

	noTools, err := Measure(FormatAnthropic, "short", nil)
	if err != nil {
		t.Fatal(err)
	}
	if noTools.Schema >= small.Schema {
		t.Errorf("dropping the tools did not remove schema cost: %d vs %d", noTools.Schema, small.Schema)
	}
}

// TestMeasureIsDeterministic pins that the measurement is a pure function of
// its inputs, so a budget derived from it cannot drift between runs.
func TestMeasureIsDeterministic(t *testing.T) {
	for _, format := range Formats() {
		a, err := Measure(format, DefaultSystemPrompt, probeSpecs())
		if err != nil {
			t.Fatal(err)
		}
		b, err := Measure(format, DefaultSystemPrompt, probeSpecs())
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Fatalf("%s: measurement is not deterministic: %+v vs %+v", format, a, b)
		}
	}
}

// TestMeasureRejectsUnknownFormat proves the format is validated rather than
// defaulted: an unknown name would otherwise be measured as something it is
// not.
func TestMeasureRejectsUnknownFormat(t *testing.T) {
	if _, err := Measure("not-a-format", DefaultSystemPrompt, probeSpecs()); err == nil {
		t.Fatal("Measure accepted an unknown wire format")
	}
}

// TestBudgetsAreDerivedAndConsistent checks the properties the two budgets must
// have, rather than their current values: each is positive, the code budget
// covers the prompt NBD-410 is specified to ship, the pair partitions the
// allowance, and consuming both keeps the spread inside the bound.
func TestBudgetsAreDerivedAndConsistent(t *testing.T) {
	code, rules := CodeBudgetTokens(), RulesBudgetTokens()
	allowance := FixedPayloadAllowanceTokens()
	bound := CumulativeSpreadBound()
	today := measuredFixedPayload()
	measured := spreadAt(float64(today))
	t.Logf("measured: fixed=%d spread=%.4f · bound=%.4f (headroom %.0f%%) · allowance=%d",
		today, measured, bound, spreadHeadroom*100, allowance)
	t.Logf("budgets: code=%d · rules=%d · sum=%d · measured+allowance=%d",
		code, rules, code+rules, today+allowance)
	t.Logf("code budget leaves a system-prompt room of %d tokens (declared allowance %d)",
		code-recordedSchemaTokens-recordedFramingTokens, systemPromptAllowanceTokens)

	if code <= 0 || rules <= 0 {
		t.Fatalf("a budget is non-positive: code=%d rules=%d", code, rules)
	}
	if allowance <= 0 {
		t.Fatalf("allowance=%d; the bound is at or below today's measured spread (%.4f), so nothing could fit", allowance, measured)
	}
	if promptRoom := code - recordedSchemaTokens - recordedFramingTokens; promptRoom < systemPromptAllowanceTokens {
		t.Errorf("the code budget leaves %d tokens for the system prompt, below the declared allowance of %d it is supposed to hold",
			promptRoom, systemPromptAllowanceTokens)
	}
	if code+rules > today+allowance {
		t.Errorf("budgets overshoot the allowance: code+rules=%d > measured+allowance=%d", code+rules, today+allowance)
	}
	if got := spreadAt(float64(code + rules)); got > bound {
		t.Errorf("consuming both budgets puts the spread at %.4f, above the bound %.4f", got, bound)
	} else {
		t.Logf("consuming both budgets: fixed=%d spread=%.4f ≤ bound %.4f", code+rules, got, bound)
	}
}
