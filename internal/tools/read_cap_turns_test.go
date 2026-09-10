package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// NBD-400 turns-per-read-cap eval.
//
// Turn accounting needs both halves: the read tool (which segments a file) and
// the loop (which spends one turn per request). internal/tools imports both, so
// the eval lives here and drives the real Registry through the real Loop.
//
// The provider is scripted, not a model. It implements exactly one strategy —
// read one truncation segment per turn, following the next_offset the previous
// result reported — because that is the mechanical lower bound on turns for any
// reader that does not guess offsets. It is not a claim about model behaviour:
// a model that guessed offsets could fetch more per turn, and a model that
// stopped early would need fewer turns and know less.
//
// Reproduce: go test ./internal/tools -run TestReadCapTurnCost -count=1 -v

var nextOffsetRE = regexp.MustCompile(`next_offset=(\d+)`)

// readState inspects the messages the loop built and reports whether a read has
// happened, whether it has more to read, and the offset to continue from.
//
// It reads the LAST tool result only, not the last next_offset it can find
// anywhere in the history: older results persist in the transcript, so scanning
// for any match would re-issue an offset that the newest read already finished
// with — a reader that loops instead of advancing.
func readState(ms []provider.Message) (offset int, more, started bool) {
	last := ""
	for _, m := range ms {
		for _, tr := range m.ToolResults {
			last = tr.Output
			started = true
		}
	}
	if !started {
		return 0, false, false
	}
	if match := nextOffsetRE.FindStringSubmatch(last); match != nil {
		if n, err := strconv.Atoi(match[1]); err == nil {
			return n, true, true
		}
	}
	return 0, false, true
}

// sequentialReader is the scripted provider described above.
type sequentialReader struct {
	path     string
	turns    int
	requests []int
}

func (p *sequentialReader) Name() string { return "scripted-sequential-reader" }

func (p *sequentialReader) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.turns++

	offset, more, started := readState(req.Messages)
	var chunks []provider.Chunk
	switch {
	case !started:
		offset = 1
		fallthrough
	case more:
		input, err := json.Marshal(map[string]any{"path": p.path, "offset": offset})
		if err != nil {
			return nil, err
		}
		p.requests = append(p.requests, offset)
		chunks = []provider.Chunk{
			{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{
				ID:    fmt.Sprintf("read-%d", p.turns),
				Name:  "read_file",
				Input: input,
			}},
			{Kind: provider.ChunkStop, Stop: "tool_use"},
		}
	default:
		chunks = []provider.Chunk{
			{Kind: provider.ChunkText, Text: "file read"},
			{Kind: provider.ChunkStop, Stop: "end_turn"},
		}
	}

	ch := make(chan provider.Chunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// allowReadsGate permits every tool. The eval is about read cost, not
// permission policy, and a denied read would silently become a no-op turn.
type allowReadsGate struct{}

func (allowReadsGate) Check(string) (agent.Verdict, string) { return agent.VerdictAllow, "" }
func (allowReadsGate) Record(string, agent.Decision)        {}
func (allowReadsGate) Effective(_ string, d agent.Decision) agent.Decision {
	return d
}

// turnCostRun is one cap's turn accounting.
type turnCostRun struct {
	capBytes   int
	turns      int
	completed  bool
	hitCeiling bool
	offsets    []int
}

// runSequentialRead drives the real loop over the fixture at capBytes and
// reports how many turns the sequential strategy needed. maxTurns is the
// ceiling in force for the run; pass a large value to measure the natural cost,
// or the shipped default to see whether the read fits inside it.
func runSequentialRead(t *testing.T, r *Registry, dir, rel string, capBytes, maxTurns int) turnCostRun {
	t.Helper()

	old := maxReadBytes
	maxReadBytes = capBytes
	defer func() { maxReadBytes = old }()

	prov := &sequentialReader{path: rel}
	sink := &recordingSink{}
	loop := &agent.Loop{
		Provider: prov,
		Tools:    r,
		Sink:     sink,
		System:   "eval",
		MaxTurns: maxTurns,
		Gate:     allowReadsGate{},
		Budget:   agent.NewBudget(),
	}
	if err := loop.Start("eval", dir); err != nil {
		t.Fatalf("cap=%d: loop.Start: %v", capBytes, err)
	}

	err := loop.Run(context.Background(), "read the fixture")

	run := turnCostRun{capBytes: capBytes, turns: prov.turns, offsets: prov.requests}
	run.hitCeiling = err == agent.ErrMaxTurns
	run.completed = err == nil
	return run
}

// recordingSink swallows every event; the loop refuses a nil sink.
type recordingSink struct{ events []agent.Event }

func (s *recordingSink) Emit(e agent.Event) error {
	s.events = append(s.events, e)
	return nil
}

// TestReadCapTurnCost measures how many turns a strictly sequential reader needs
// to read the fixture at each cap, and whether that fits the shipped MaxTurns
// default of 12. The gap it exposes is the reason this stage does not raise the
// cap on the eval's say-so: a larger cap would mask an unbounded-turn risk
// rather than remove it.
func TestReadCapTurnCost(t *testing.T) {
	r, dir := newReg(t)
	rel, fileBytes := evalFixture(t, dir, evalLineCount)

	// The shipped default, read from the loop rather than restated here.
	const shippedMaxTurns = 12

	t.Logf("fixture: %d lines, %d bytes; shipped MaxTurns default: %d", evalLineCount, fileBytes, shippedMaxTurns)
	t.Logf("%8s %7s %9s %s", "cap", "turns", "fits_12", "offsets")

	for _, cap := range evalCaps {
		// Measure the natural cost first, with a ceiling high enough not to
		// interfere; then decide whether that cost fits the shipped default.
		natural := runSequentialRead(t, r, dir, rel, cap, 4096)
		if !natural.completed {
			t.Fatalf("cap=%d: natural run did not complete", cap)
		}
		fits := natural.turns <= shippedMaxTurns
		t.Logf("%8d %7d %9v %v", cap, natural.turns, fits, natural.offsets)

		if natural.turns <= 0 {
			t.Errorf("cap=%d: no turns recorded", cap)
		}
		// Offsets must march forward: the reader only ever continues.
		for i := 1; i < len(natural.offsets); i++ {
			if natural.offsets[i] <= natural.offsets[i-1] {
				t.Errorf("cap=%d: offsets not increasing: %v", cap, natural.offsets)
				break
			}
		}
	}

	// The shipped combination does NOT fit the shipped ceiling for this
	// fixture, and that is the stage's finding rather than an assumption:
	// reading a mid-sized file by sequential calls needs more turns than the
	// default allows. It is recorded in docs/TECH_DEBT.md (READ_CAP_TURN_COST)
	// and asserted here as a tripwire — if it ever starts fitting, a default
	// moved, and the record must be updated with the reason instead of the
	// change landing silently.
	shipped := runSequentialRead(t, r, dir, rel, defaultMaxRead(), shippedMaxTurns)
	if shipped.completed {
		t.Fatalf("the shipped cap %d now reads the %d-line fixture within MaxTurns=%d (%d turns); "+
			"this contradicts TECH_DEBT READ_CAP_TURN_COST — update that record and say what moved",
			defaultMaxRead(), evalLineCount, shippedMaxTurns, shipped.turns)
	}
	if !shipped.hitCeiling {
		t.Fatalf("shipped run neither completed nor hit the turn ceiling: %+v", shipped)
	}
	t.Logf("shipped combination: cap=%d hits MaxTurns=%d after %d turns (the recorded limitation)",
		defaultMaxRead(), shippedMaxTurns, shipped.turns)

	// The contrast that makes the finding actionable: a larger cap fits, so
	// the documented escape hatch (NABD_MAX_READ) is a real remedy.
	bigger := runSequentialRead(t, r, dir, rel, 8192, shippedMaxTurns)
	if !bigger.completed {
		t.Fatalf("cap 8192 did not fit MaxTurns=%d (%d turns); the escape hatch does not work", shippedMaxTurns, bigger.turns)
	}
	t.Logf("escape hatch: cap=8192 completes in %d turns within MaxTurns=%d", bigger.turns, shippedMaxTurns)
}
