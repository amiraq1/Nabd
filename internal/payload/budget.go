package payload

// Budgets.
//
// Two budgets, because two different owners grow the fixed payload:
//
//   - CodeBudgetTokens limits what the CODE owns: the system prompt, the tool
//     schemas, and the wire framing. It is sized for the prompt NBD-410 will
//     introduce, not for today's one-line stub.
//   - RulesBudgetTokens limits what the USER owns: a project's AGENTS.md
//     instructions, which NBD-410 will append. It has no consumer yet, and
//     that is deliberate — 410 spends this budget instead of inventing a
//     ceiling of its own.
//
// One shared ceiling for both is not a guard: sized for the prompt, it leaves
// nothing for rules and gets raised every stage; sized for rules, it stops
// noticing code growth. Splitting the owners makes each failure say which
// owner grew.
//
// Both derive from named inputs: the recorded measurements below, the declared
// system-prompt allowance, and the declared cost headroom. No budget here is
// typed in as a standalone figure — the derivation is in the code so a
// reviewer recomputes it instead of trusting it.

// Recorded measurements. Each is reproducible; the reproducing test is named.
const (
	// TestFixedPayloadMeasurementsMatchTheWire in cmd/ag, at the commit that
	// introduced this package: anthropic 752, openai-compatible 809 (encoded
	// totals, model included).
	recordedFixedPayloadAnthropic = 752
	recordedFixedPayloadOpenAI    = 809
	// The same measurement's components, for the larger format. Residue is the
	// outer braces and key punctuation, which belong to no single field: it is
	// budgeted for rather than dropped, so the code budget bounds the encoded
	// request and not just its named parts.
	recordedSchemaTokens  = 704 // openai-compatible
	recordedFramingTokens = 36  // openai-compatible, envelope only (the model is a separate component)
	recordedResidueTokens = 1   // worst residue observed across both formats
	// TestReadCapCumulativeCost in internal/tools: the worst and best columns
	// of the read-cap table, which is what the cost bound below is stated
	// against.
	recordedWorstRequests = 15
	recordedWorstHistory  = 47136
	recordedBestRequests  = 3
	recordedBestHistory   = 16510
)

// Declared inputs.
const (
	// systemPromptAllowanceTokens sizes the code budget for the prompt
	// NBD-410 will ship. Its spec puts the real system.md at 800–1500 tokens;
	// the allowance is the upper bound, and the guard is expected to have
	// headroom over today's stub because that is what the budget is for.
	systemPromptAllowanceTokens = 1500

	// spreadHeadroom is how much worse than today the cumulative spread may
	// get before the cost bound trips. It replaces a flat 4.0 that, against a
	// measured 3.11, was loose by 29% and could not notice a thousand-token
	// inflation.
	spreadHeadroom = 0.15
)

// measuredFixedPayload is the largest fixed payload measured today.
func measuredFixedPayload() int {
	if recordedFixedPayloadOpenAI > recordedFixedPayloadAnthropic {
		return recordedFixedPayloadOpenAI
	}
	return recordedFixedPayloadAnthropic
}

// spreadAt returns the cumulative worst÷best ratio when every request carries
// a fixed payload of fixedTokens, from the recorded columns:
//
//	ratio(O) = (worstRequests·O + worstHistory) / (bestRequests·O + bestHistory)
//
// It is monotone increasing in O with asymptote worstRequests/bestRequests
// (5.00 today): the fixed payload is multiplied by the request count, which is
// why this term and not the history is the lever.
func spreadAt(fixedTokens float64) float64 {
	worst := float64(recordedWorstRequests)*fixedTokens + recordedWorstHistory
	best := float64(recordedBestRequests)*fixedTokens + recordedBestHistory
	return worst / best
}

// CumulativeSpreadBound is the ratio the cumulative measurement must stay
// under: today's measured spread plus the declared headroom.
func CumulativeSpreadBound() float64 {
	return spreadAt(float64(measuredFixedPayload())) * (1 + spreadHeadroom)
}

// FixedPayloadAllowanceTokens is how many tokens may be added to every request
// before the cumulative spread reaches the bound. It is the total the two
// budgets below partition.
//
// Solving spreadAt(O0 + Δ) = bound for Δ:
//
//	Δ = (bestHistory·bound − worstHistory) / (worstRequests − bestRequests·bound) − O0
func FixedPayloadAllowanceTokens() int {
	bound := CumulativeSpreadBound()
	o0 := float64(measuredFixedPayload())
	x := (float64(recordedBestHistory)*bound - float64(recordedWorstHistory)) /
		(float64(recordedWorstRequests) - float64(recordedBestRequests)*bound)
	return int(x - o0)
}

// CodeBudgetTokens is the ceiling for the code-owned payload: the system
// prompt, the tool schemas and the framing.
//
//	codeBudget = round_up_100(systemPromptAllowance + recordedSchema + recordedFraming + recordedResidue)
//
// Rounded UP because this budget carries an obligation: it must fit the prompt
// NBD-410 is specified to ship. Rounding it down would leave a budget that
// cannot hold its own declared allowance.
func CodeBudgetTokens() int {
	need := systemPromptAllowanceTokens + recordedSchemaTokens + recordedFramingTokens + recordedResidueTokens
	if need%100 == 0 {
		return need
	}
	return need/100*100 + 100
}

// RulesBudgetTokens is the ceiling for a project's AGENTS.md layer (NBD-410).
//
//	rulesBudget = round_down_100(measuredFixedPayload + allowance − codeBudget)
//
// It is the remainder of the cost allowance after the code's share, so it is
// rounded DOWN and the pair can never exceed the allowance: codeBudget rounds
// up by at most 99 and the remainder floors, so codeBudget + rulesBudget ≤
// measured + allowance. Rounding this one up instead would let the two budgets
// together push the spread past CumulativeSpreadBound.
//
// It has no consumer today. That is deliberate — NBD-410 spends this budget
// instead of inventing a ceiling of its own.
func RulesBudgetTokens() int {
	room := measuredFixedPayload() + FixedPayloadAllowanceTokens() - CodeBudgetTokens()
	if room < 0 {
		return 0
	}
	return room / 100 * 100
}
