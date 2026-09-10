package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
)

// NBD-400 read-cap eval.
//
// The question this answers with measurement, not opinion: what does the
// read_file byte cap cost in tool calls, prompt tokens and wall time, for a
// file the agent actually meets? It is deterministic and offline — no provider
// is contacted, so it runs identically on Termux and in CI.
//
// What it does NOT answer: whether a larger cap survives a real provider's
// tokens-per-minute ceiling. That bound is provider-specific (the shipped
// constants are measured against an 8000 TPM free key) and is why the shipped
// default is unchanged; see read.go's derivation comment.
//
// Reproduce: go test ./internal/tools -run TestReadCapEval -count=1 -v

// evalLineShapes are the line shapes the fixture cycles through. They are
// deliberately mixed: uniform short lines would understate the cap's effect,
// and uniform long lines would exaggerate it.
var evalLineShapes = []func(i int) string{
	func(i int) string { return "\t// step " + strconv.Itoa(i) + " keeps the pipeline honest and readable" },
	func(i int) string { return "\tif err := step" + strconv.Itoa(i) + "(ctx, input, opts); err != nil {" },
	func(i int) string { return "\t\treturn fmt.Errorf(\"step " + strconv.Itoa(i) + ": %w\", err)" },
	func(i int) string { return "\t}" },
	func(i int) string {
		return "\tvalue" + strconv.Itoa(i) + " := compute(" + strconv.Itoa(i) + ", options{retries: 3, timeout: time.Second, verbose: false})"
	},
}

// evalFixture writes a deterministic, Go-like source file of n lines into dir
// and returns its name and byte size.
func evalFixture(t *testing.T, dir string, n int) (string, int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(evalLineShapes[(i-1)%len(evalLineShapes)](i))
		b.WriteByte('\n')
	}
	name := "fixture.go"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return name, b.Len()
}

// readCapRun is one cap's measured outcome. longestCall is the biggest single
// tool result: the per-request input that a provider's TPM ceiling sees.
type readCapRun struct {
	capBytes    int
	calls       int
	delivered   int
	linesRead   int
	tokensEst   int
	longestCall int
	elapsed     time.Duration
}

// walkRead reads the whole file through read_file at capBytes, following the
// next_offset each truncation reports, and measures the cost.
//
// Invariants it enforces (so this is a regression test, not only a report):
//   - offsets advance strictly, so no segment is read twice or stalls;
//   - the read completes rather than looping forever;
//   - the union of lines_read equals the file's line count (no gap, no overlap).
func walkRead(t *testing.T, r *Registry, rel string, totalLines, capBytes int) readCapRun {
	t.Helper()

	old := maxReadBytes
	maxReadBytes = capBytes
	defer func() { maxReadBytes = old }()

	var run readCapRun
	run.capBytes = capBytes
	offset := 1
	seenOffsets := map[int]bool{}
	start := time.Now()

	const maxCalls = 4096 // a tripwire, not a limit: a stall must fail loudly
	for i := 0; i < maxCalls; i++ {
		if seenOffsets[offset] {
			t.Fatalf("cap=%d: offset %d revisited — the walk is not making progress", capBytes, offset)
		}
		seenOffsets[offset] = true

		raw, err := json.Marshal(map[string]any{"path": rel, "offset": offset})
		if err != nil {
			t.Fatal(err)
		}
		out, err := r.RunDetailed(context.Background(), "read_file", raw)
		if err != nil {
			t.Fatalf("cap=%d call=%d: %v", capBytes, i, err)
		}
		if !out.OK {
			t.Fatalf("cap=%d call=%d: read not OK: %q", capBytes, i, out.Text)
		}

		run.calls++
		run.delivered += len(out.Text)
		run.linesRead += out.LinesRead
		if n := len(out.Text); n > run.longestCall {
			run.longestCall = n
		}
		// The prompt cost of this result as the loop would serialise it: the
		// same fence the provider boundary applies.
		run.tokensEst += agent.EstimateText(agent.FenceToolOutput("read_file", out.Text))

		if !out.Truncated {
			run.elapsed = time.Since(start)
			if run.linesRead != totalLines {
				t.Fatalf("cap=%d: union of lines_read = %d, want %d (gap or overlap)", capBytes, run.linesRead, totalLines)
			}
			return run
		}
		if out.NextOffset <= offset {
			t.Fatalf("cap=%d: next_offset did not advance (%d → %d)", capBytes, offset, out.NextOffset)
		}
		offset = out.NextOffset
	}
	t.Fatalf("cap=%d: read of %d lines did not complete within %d calls", capBytes, totalLines, maxCalls)
	return run
}

// evalCaps are the candidate read caps. 3072 is the shipped default; the rest
// are the sizes considered during this review.
var evalCaps = []int{3072, 8192, 16384, 24576}

// evalLineCount sizes the fixture: 800 mixed lines is roughly a mid-sized Go
// file (~40 KB).
const evalLineCount = 800

// TestReadCapEval measures, for each candidate read cap, the tool calls, bytes,
// estimated prompt tokens and wall time needed to read the fixture to
// completion. Log-only by design — the numbers are the deliverable — while the
// assertions in walkRead guard the contract they rest on.
func TestReadCapEval(t *testing.T) {
	r, dir := newReg(t)
	rel, fileBytes := evalFixture(t, dir, evalLineCount)

	t.Logf("fixture: %d lines, %d bytes; caps: %v", evalLineCount, fileBytes, evalCaps)
	t.Logf("%8s %7s %9s %8s %9s %10s %10s", "cap", "calls", "delivered", "lines", "tok_est", "longest", "elapsed")
	runs := make([]readCapRun, 0, len(evalCaps))
	for _, c := range evalCaps {
		run := walkRead(t, r, rel, evalLineCount, c)
		runs = append(runs, run)
		t.Logf("%8d %7d %9d %8d %9d %10d %10s",
			run.capBytes, run.calls, run.delivered, run.linesRead, run.tokensEst, run.longestCall, run.elapsed)
	}

	// The mechanical relationships the numbers expose. Read them as a
	// reframing, because they are not the intuitive ones:
	//
	//   - calls fall as the cap rises (14 → 2): fewer round trips.
	//   - longestCall rises with the cap: that is the per-request input a
	//     provider's TPM ceiling actually sees, and the only real cost of a
	//     larger cap. It is why this measurement alone cannot justify raising
	//     the shipped default — the ceiling is provider-specific.
	//   - delivered bytes and estimated tokens FALL as the cap rises, because
	//     each truncation re-sends a tail. The small cap is not the cheap one
	//     in total tokens; it is the cheap one per request.
	for i := 1; i < len(runs); i++ {
		prev, cur := runs[i-1], runs[i]
		if cur.calls > prev.calls {
			t.Errorf("cap %d needs more calls (%d) than cap %d (%d)", cur.capBytes, cur.calls, prev.capBytes, prev.calls)
		}
		if cur.longestCall < prev.longestCall {
			t.Errorf("cap %d produced a smaller largest call (%d) than cap %d (%d)", cur.capBytes, cur.longestCall, prev.capBytes, prev.longestCall)
		}
		if cur.delivered > prev.delivered {
			t.Errorf("cap %d delivered more bytes (%d) than cap %d (%d): truncation overhead should shrink, not grow", cur.capBytes, cur.delivered, prev.capBytes, prev.delivered)
		}
		if cur.tokensEst > prev.tokensEst {
			t.Errorf("cap %d estimated more tokens (%d) than cap %d (%d)", cur.capBytes, cur.tokensEst, prev.capBytes, prev.tokensEst)
		}
	}
	// Every cap must reach EOF: none may strand the reader.
	if runs[len(runs)-1].linesRead != evalLineCount {
		t.Errorf("largest cap read %d lines, want %d", runs[len(runs)-1].linesRead, evalLineCount)
	}
	// The shipped default must reach the whole fixture — a cap that strands
	// the reader would make the turn analysis below meaningless.
	if runs[0].linesRead != evalLineCount {
		t.Errorf("shipped cap %d read %d lines, want %d", runs[0].capBytes, runs[0].linesRead, evalLineCount)
	}
}
