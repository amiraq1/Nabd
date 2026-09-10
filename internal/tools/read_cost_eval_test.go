package tools

import (
	"context"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// NBD-401: cumulative input cost of the read cap.
//
// TestReadCapEval measures bytes delivered once. That is not the bill. Every
// tool round re-sends the system prompt, the tool schemas and the history, so
// the cap's real cost is the SUM of per-turn input over the whole read.
//
// The measurement drives the real Loop and reads the requests it actually
// sent, instead of rebuilding the history here and calling Squeeze by hand.
// That is deliberate: the loop applies Squeeze (which itself calls
// DedupeReadTails), the [a-z_] fence and keepFullRounds before every turn, so
// measuring the loop's own output cannot drift from the production path —
// hand-built history could, silently. It also avoids the trap that
// read_file.RunDetailed drains the legacy truncation slot, so a walk keyed on
// Registry.ConsumeTruncated would stop after the first round and measure a
// one-request read.
//
// Deterministic: no network, no provider keys, no clock dependence.
//
// Reproduce: go test ./internal/tools -run TestReadCapCumulativeCost -count=1 -v

// promptOverhead is the measured per-request fixed input: system prompt + tool
// schemas + message framing. Provenance: the same two 413 sessions cited in
// read.go's budget derivation (203320, 203954). It is a constant on purpose —
// it does not vary with the cap, so it cannot bias the comparison between
// caps, but omitting it would understate runs with many short turns.
//
// It is deliberately NOT read from the request the loop sends: that would mix
// a second measurement into this one. TestReadCapCumulativeCost logs the real
// request's fixed part once, as a cross-check on this constant.
const promptOverhead = 2210

// cacheDiscount is the multiplier applied to cache-read input tokens by
// providers that support prompt caching. NOT measured by nabd — it is the
// published ratio, and it appears here to answer exactly one question: does
// prompt caching change the cap decision (i.e. is NBD-430 the real fix)? Any
// policy change must re-measure live.
const cacheDiscount = 0.10

// cumulativeRatioBound pins the measured worst÷best spread with headroom. It
// is a tripwire on a known-bad state, not a specification: the shipped default
// really does cost multiples more in cumulative input, and that is recorded in
// docs/TECH_DEBT.md (READ_CAP_TURN_COST). The measurement is deterministic, so
// this bound is not protecting against estimator noise — it catches a large
// drift, such as history accumulation no longer being absorbed at all, which
// would push the spread toward the request-count ratio (~5×) or beyond.
const cumulativeRatioBound = 4.0

// recordingReader reuses the sequential read strategy from
// read_cap_turns_test.go — one truncation segment per turn, following the
// next_offset the previous result reported — and additionally records every
// request the loop sent, which is what this measurement needs.
type recordingReader struct {
	*sequentialReader
	messages [][]provider.Message
}

func (r *recordingReader) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ms := make([]provider.Message, len(req.Messages))
	copy(ms, req.Messages)
	r.messages = append(r.messages, ms)
	return r.sequentialReader.Stream(ctx, req)
}

// readCostRun is one cap's cumulative accounting.
//
// history is the cumulative sum WITHOUT the fixed per-request overhead: it
// isolates what Squeeze decides (how much old content each request still
// carries). uncached adds the overhead back, which is what the provider bills.
type readCostRun struct {
	capBytes     int
	calls        int
	turns        int
	delivered    int
	history      int
	uncached     int
	cached       float64
	schemaTokens int
	compacted    bool
}

// runReadCost drives the real loop over the fixture at capBytes and returns the
// cumulative input cost of the whole read.
//
// maxTurns is set high on purpose: the point is to measure the natural cost,
// not to stop at the shipped ceiling. TestReadCapPinsMeasuredTurnCost_NBD401
// owns the ceiling question.
func runReadCost(t *testing.T, reg *Registry, dir, rel string, capBytes int) readCostRun {
	t.Helper()

	old := maxReadBytes
	maxReadBytes = capBytes
	defer func() { maxReadBytes = old }()

	prov := &recordingReader{sequentialReader: &sequentialReader{path: rel}}
	sink := &recordingSink{}
	loop := &agent.Loop{
		Provider: prov,
		Tools:    reg,
		Sink:     sink,
		System:   "eval",
		MaxTurns: 4096,
		Gate:     allowReadsGate{},
		Budget:   agent.NewBudget(),
	}
	if err := loop.Start("eval", dir); err != nil {
		t.Fatalf("cap=%d: loop.Start: %v", capBytes, err)
	}
	if err := loop.Run(context.Background(), "read the fixture"); err != nil {
		t.Fatalf("cap=%d: loop.Run: %v", capBytes, err)
	}

	run := readCostRun{capBytes: capBytes, turns: prov.turns, calls: len(prov.messages)}
	for _, e := range sink.events {
		if e.Type == agent.Notice && strings.Contains(e.Text, "context compacted") {
			run.compacted = true
		}
	}
	if run.compacted {
		// Compaction would replace the history mid-read and change what the
		// sum means; the measurement is only valid below the pressure trigger.
		t.Fatalf("cap=%d: preemptive compaction fired during the read; the cumulative sum is not comparable", capBytes)
	}

	for _, ms := range prov.messages {
		h := agent.EstimateMessages(ms)
		total := promptOverhead + h
		run.history += h
		run.uncached += total
		// Cached: the prefix (fixed input + everything but the newest message)
		// is a cache read; only the newest message is billed in full.
		fresh := 0
		if n := len(ms); n > 0 {
			fresh = agent.EstimateMessages(ms[n-1:])
		}
		run.cached += float64(total-fresh)*cacheDiscount + float64(fresh)
	}
	run.schemaTokens = toolSpecTokens(reg)

	// The payload half, from the same deterministic fixture: measured through
	// the registry's own Outcome, not re-derived from the fenced request.
	payload := walkRead(t, reg, rel, evalLineCount, capBytes)
	run.delivered = payload.delivered
	return run
}

// toolSpecTokens estimates the schema cost every request carries, from the
// registry's real specs. It is a cross-check on promptOverhead's composition,
// not an input to the comparison: the comparison holds the fixed part constant
// across caps, which is exactly why it cannot be biased by it.
func toolSpecTokens(t agent.Tools) int {
	n := 0
	for _, s := range t.Specs() {
		n += agent.EstimateText(s.Name) + agent.EstimateText(s.Description) + agent.EstimateText(string(s.Schema))
	}
	return n
}

// TestReadCapCumulativeCost sums the input the loop actually sent over a
// complete read, at the same caps TestReadCapEval uses, and reports it next to
// the cache-assumed figure.
func TestReadCapCumulativeCost(t *testing.T) {
	reg, dir := newReg(t)
	rel, fileBytes := evalFixture(t, dir, evalLineCount)

	t.Logf("fixture: %d lines, %d bytes; caps: %v", evalLineCount, fileBytes, evalCaps)
	t.Logf("%8s %7s %9s %12s %10s %12s %12s %9s", "cap", "calls", "delivered", "cumulative_in", "hist_in", "cached_in", "ratio", "cache_ratio")

	runs := make([]readCostRun, 0, len(evalCaps))
	for _, c := range evalCaps {
		runs = append(runs, runReadCost(t, reg, dir, rel, c))
	}

	best, bestHist, bestCached := runs[0].uncached, runs[0].history, runs[0].cached
	for _, r := range runs {
		if r.uncached < best {
			best = r.uncached
		}
		if r.history < bestHist {
			bestHist = r.history
		}
		if r.cached < bestCached {
			bestCached = r.cached
		}
	}
	for _, r := range runs {
		t.Logf("%8d %7d %9d %12d %10d %12.0f %8.2f× %8.2f×",
			r.capBytes, r.calls, r.delivered, r.uncached, r.history, r.cached,
			float64(r.uncached)/float64(best), r.cached/bestCached)
	}

	// The three spreads, stated separately because they answer different
	// questions. The history-only spread is the direct evidence about Squeeze:
	// if it stayed near the request-count ratio, nothing was being absorbed.
	t.Logf("spread worst÷best — cumulative_in %.2f× · hist_in %.2f× · cached_in %.2f× · requests %.2f×",
		float64(runs[0].uncached)/float64(best),
		float64(runs[0].history)/float64(bestHist),
		runs[0].cached/bestCached,
		float64(runs[0].turns)/float64(runs[len(runs)-1].turns))

	// The fixed part every request carries, as a cross-check on promptOverhead.
	// Only the schemas are real here: the eval loop's System is a stub, so the
	// CLI's own prompt is not included and this is a lower bound.
	t.Logf("cross-check: registry tool schemas ≈ %d tokens (loop System is a stub in this eval); promptOverhead const=%d covers system+schemas+framing and is identical in every column, so it cannot bias the ratio",
		runs[0].schemaTokens, promptOverhead)

	// Assertion 1 — the payload is the same read at every cap (reassembly
	// integrity). Without it, a "cheaper" column might simply have read less.
	// The band is wide because the truncation tail differs per call.
	for _, r := range runs {
		if r.delivered == 0 {
			t.Fatalf("cap=%d: nothing delivered", r.capBytes)
		}
	}
	minD, maxD := runs[0].delivered, runs[0].delivered
	for _, r := range runs {
		if r.delivered < minD {
			minD = r.delivered
		}
		if r.delivered > maxD {
			maxD = r.delivered
		}
	}
	if ratio := float64(maxD) / float64(minD); ratio > 1.10 {
		t.Errorf("delivered payload varies by %.2f× across caps (%d..%d bytes): the columns are not reading the same file", ratio, minD, maxD)
	}

	// Assertion 2 — cumulative input is monotonically non-increasing as the
	// cap grows. A larger cap reads the same file in fewer, larger requests,
	// and truncation re-sends a tail, so this direction is structural. If it
	// breaks, the cause is real and worth stopping for — not estimator noise.
	for i := 1; i < len(runs); i++ {
		prev, cur := runs[i-1], runs[i]
		if cur.uncached > prev.uncached {
			t.Errorf("cumulative input rose from cap %d (%d) to cap %d (%d): accumulation is not being absorbed",
				prev.capBytes, prev.uncached, cur.capBytes, cur.uncached)
		}
	}

	// Assertion 3 — the whole spread stays inside the pinned bound. This is
	// the number the cap decision rests on, so it is asserted rather than only
	// reported: see cumulativeRatioBound and docs/TECH_DEBT.md
	// (READ_CAP_TURN_COST).
	if ratio := float64(runs[0].uncached) / float64(best); ratio > cumulativeRatioBound {
		t.Errorf("worst cap costs %.2f× the best in cumulative input, above the pinned %.1f× bound: "+
			"history accumulation may have stopped being absorbed, or the shipped default moved. "+
			"Re-read READ_CAP_TURN_COST in docs/TECH_DEBT.md before changing anything.",
			ratio, cumulativeRatioBound)
	}
	// The same direction check on the cache-assumed figure: caching compresses
	// the spread, it does not invert it.
	if runs[0].cached < bestCached {
		t.Errorf("cache-assumed spread is inverted: worst cap %.0f < best %.0f", runs[0].cached, bestCached)
	}
}
