package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/snap"
)

// writeVia runs a write through the real Registry and returns the recorded
// EditRecord, exercising the read→write metadata handoff end to end.
func writeVia(t *testing.T, r *Registry, name, content string) *agent.EditRecord {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"path": name, "content": content})
	if _, ok, err := r.Run(context.Background(), providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}
	return r.LastEdit()
}

// TestReadReportsOwnLineCount (C#1): read_file records its own line count in
// the Outcome; it never writes the shared linesRead slot (proven by a consume
// that stays 0 after the read).
func TestReadReportsOwnLineCount(t *testing.T) {
	r, dir := newReg(t)
	f := filepath.Join(dir, "five.md")
	os.WriteFile(f, []byte("1\n2\n3\n4\n5\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "five.md"})
	out, err := r.RunDetailed(context.Background(), "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}
	if out.LinesRead != 5 {
		t.Fatalf("out.LinesRead=%d, want 5", out.LinesRead)
	}
	// The read must not have polluted the write-side slot.
	if got := r.ConsumeLinesRead(); got != 0 {
		t.Fatalf("read wrote the shared slot=%d, want 0 (reads are result-scoped)", got)
	}
}

// TestSubsequentWriteReportsZeroReadLines (C#2): after a read is staged for the
// next write by the loop (SetLinesRead) and consumed, a later blind write must
// carry 0 — the count cannot survive to an unrelated later operation.
func TestSubsequentWriteReportsZeroReadLines(t *testing.T) {
	r, _ := newReg(t)
	// read 3 lines
	f := filepath.Join(r.root.Dir(), "a.md")
	os.WriteFile(f, []byte("a\nb\nc\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "a.md"})
	out, err := r.RunDetailed(context.Background(), "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}
	if out.LinesRead != 3 {
		t.Fatalf("out.LinesRead=%d, want 3", out.LinesRead)
	}
	// The loop stages the count for the next write; the write consumes it.
	r.SetLinesRead(out.LinesRead)
	// first write consumes the 3
	if rec := writeVia(t, r, "a.md", "x\n"); rec.ReadLines != 3 {
		t.Fatalf("first write ReadLines=%d, want 3", rec.ReadLines)
	}
	// second write is blind: must be 0, not the stale 3
	if rec := writeVia(t, r, "a.md", "y\n"); rec.ReadLines != 0 {
		t.Fatalf("subsequent write ReadLines=%d, want 0 (no stale carry-over)", rec.ReadLines)
	}
}

// TestTwoSequentialReadsDoNotAccumulate (C#3): read 3 then read 5, a write
// must carry 5 (the latest), not 8. The loop re-stages the count on every
// read via SetLinesRead, so the latest read wins — exactly what the next commit
// consumes.
func TestTwoSequentialReadsDoNotAccumulate(t *testing.T) {
	r, dir := newReg(t)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("1\n2\n3\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("1\n2\n3\n4\n5\n"), 0o644)
	for _, p := range []string{"a.md", "b.md"} {
		raw, _ := json.Marshal(map[string]any{"path": p})
		out, err := r.RunDetailed(context.Background(), "read_file", raw)
		if err != nil || !out.OK {
			t.Fatalf("read %s: ok=%v err=%v", p, out.OK, err)
		}
		r.SetLinesRead(out.LinesRead) // the loop re-stages per read; latest wins
	}
	if rec := writeVia(t, r, "b.md", "x\n"); rec.ReadLines != 5 {
		t.Fatalf("ReadLines=%d, want 5 (latest read, not accumulated 8)", rec.ReadLines)
	}
}

// TestTruncatedReadReportsOnlyItsInvocation (C#4/5): a read that is
// truncated reports truncation only for its own invocation; the next clean
// read must carry no truncation.
func TestTruncatedReadReportsOnlyItsInvocation(t *testing.T) {
	r, dir := newReg(t)
	big := filepath.Join(dir, "big.go")
	var b []byte
	for i := 0; i < 400; i++ {
		b = append(b, []byte("line number "+string(rune('a'+i%26))+"\n")...)
	}
	os.WriteFile(big, b, 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "big.go"})
	out, err := r.RunDetailed(context.Background(), "read_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Fatalf("first read not truncated, want truncated")
	}
	// the flag was consumed by RunDetailed: a second consume is empty
	if trunc, _ := r.ConsumeTruncated(); trunc {
		t.Fatalf("truncation survived its invocation consumer")
	}

	small := filepath.Join(dir, "small.md")
	os.WriteFile(small, []byte("x\n"), 0o644)
	raw2, _ := json.Marshal(map[string]any{"path": "small.md"})
	out2, err := r.RunDetailed(context.Background(), "read_file", raw2)
	if err != nil {
		t.Fatal(err)
	}
	if out2.Truncated {
		t.Fatalf("clean read after truncated read carried truncation flag")
	}
}

// TestFailedReadDoesNotContaminate (C#6): a failed read must leave no metadata
// — its Outcome carries nothing, and it must not pollute the shared slots a
// later call could inherit.
func TestFailedReadDoesNotContaminate(t *testing.T) {
	r, _ := newReg(t)
	// a successful read records its count in the Outcome (not the slot)
	f := filepath.Join(r.root.Dir(), "ok.md")
	os.WriteFile(f, []byte("1\n2\n3\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "ok.md"})
	out, err := r.RunDetailed(context.Background(), "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}
	if out.LinesRead != 3 {
		t.Fatalf("out.LinesRead=%d, want 3", out.LinesRead)
	}
	if got := r.ConsumeLinesRead(); got != 0 {
		t.Fatalf("successful read polluted the shared slot=%d, want 0", got)
	}

	// failed read (missing file) must leave no metadata in the Outcome either
	rawBad, _ := json.Marshal(map[string]any{"path": "nope.md"})
	bad, err := r.RunDetailed(context.Background(), "read_file", rawBad)
	if err == nil || bad.OK {
		t.Fatal("expected error reading missing file")
	}
	if bad.LinesRead != 0 || bad.Truncated || bad.NextOffset != 0 {
		t.Fatalf("failed read left metadata in Outcome: %+v", bad)
	}
	if got := r.ConsumeLinesRead(); got != 0 {
		t.Fatalf("failed read left linesRead=%d, want 0", got)
	}
	if trunc, _ := r.ConsumeTruncated(); trunc {
		t.Fatalf("failed read left truncation flag")
	}
}

// TestCancelledOperationDoesNotContaminate (C#7): a read whose context is
// cancelled must not leave metadata for the next action.
func TestCancelledOperationDoesNotContaminate(t *testing.T) {
	r, dir := newReg(t)
	f := filepath.Join(dir, "c.md")
	os.WriteFile(f, []byte("1\n2\n3\n4\n"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	raw, _ := json.Marshal(map[string]any{"path": "c.md"})
	out, err := r.RunDetailed(ctx, "read_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Truncated {
		t.Fatalf("cancelled read reported truncation")
	}
	if got := r.ConsumeLinesRead(); got != 0 {
		t.Fatalf("cancelled read left linesRead=%d, want 0", got)
	}
}

// TestConcurrentToolMetadataIsInvocationScoped (replaces C#8): under the
// result-scoped model, a shared Registry MUST keep each invocation's metadata
// isolated even when reads and a write overlap. The metadata lives in the
// Outcome (computed locally in run()), so it cannot cross between concurrent
// invocations — no shared-slot read-side handoff to race on. Four scenarios,
// all forced through a tight sync barrier so goroutines actually overlap (no
// time.Sleep), and every assertion stays quiet under -race:
//  1. two concurrent reads on different files (one truncated, one complete)
//     each report their OWN count + next_offset.
//  2. a truncated read and a complete read running together do not exchange
//     their Truncated flag.
//  3. a read and a write racing: the write never steals the read's count,
//     because the read no longer writes the shared linesRead slot at all.
func TestConcurrentToolMetadataIsInvocationScoped(t *testing.T) {
	r, dir := newReg(t)
	// File A: large enough to truncate at the byte cap.
	big := filepath.Join(dir, "big.go")
	var bb strings.Builder
	for i := 0; i < 400; i++ {
		bb.WriteString(strings.Repeat("a", 120) + "\n")
	}
	if err := os.WriteFile(big, []byte(bb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	// File B: small, complete read (5 lines, no truncation).
	small := filepath.Join(dir, "small.md")
	if err := os.WriteFile(small, []byte("1\n2\n3\n4\n5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// File C: read+write race scenario.
	mid := filepath.Join(dir, "mid.md")
	if err := os.WriteFile(mid, []byte("x\ny\nz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --- Scenarios 1 & 2: concurrent read A (truncated) vs read B (complete) ---
	var (
		mu       sync.Mutex
		problems []string
		readAOut agent.Outcome
		readBOut agent.Outcome
	)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-gate
		raw, _ := json.Marshal(map[string]any{"path": "big.go"})
		out, err := r.RunDetailed(context.Background(), "read_file", raw)
		mu.Lock()
		readAOut, _ = out, err
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		<-gate
		raw, _ := json.Marshal(map[string]any{"path": "small.md"})
		out, err := r.RunDetailed(context.Background(), "read_file", raw)
		mu.Lock()
		readBOut, _ = out, err
		mu.Unlock()
	}()
	close(gate)
	wg.Wait()

	if readAOut.Truncated != true {
		problems = append(problems, "readA: Truncated=false, want true")
	}
	if readAOut.LinesRead == 0 {
		problems = append(problems, "readA: LinesRead=0")
	}
	if readAOut.NextOffset <= 0 {
		problems = append(problems, "readA: NextOffset<=0")
	}
	if readBOut.Truncated != false {
		problems = append(problems, "readB: Truncated=true, want false (no exchange)")
	}
	if readBOut.LinesRead != 5 {
		problems = append(problems, fmt.Sprintf("readB: LinesRead=%d, want 5 (no exchange)", readBOut.LinesRead))
	}
	if readBOut.NextOffset != 0 {
		problems = append(problems, fmt.Sprintf("readB: NextOffset=%d, want 0", readBOut.NextOffset))
	}
	if len(problems) != 0 {
		t.Fatalf("scenario 1/2 isolation failed: %v", problems)
	}

	// --- Scenario 3: concurrent read + write on the shared slot.
	// The read must not write the shared linesRead slot, so the write can never
	// steal the read's count.
	gate2 := make(chan struct{})
	var (
		wg2     sync.WaitGroup
		readOut agent.Outcome
		writeOK bool
	)
	wg2.Add(2)
	go func() {
		defer wg2.Done()
		<-gate2
		raw, _ := json.Marshal(map[string]any{"path": "mid.md"})
		out, _ := r.RunDetailed(context.Background(), "read_file", raw)
		mu.Lock()
		readOut = out
		mu.Unlock()
	}()
	go func() {
		defer wg2.Done()
		<-gate2
		raw, _ := json.Marshal(map[string]any{"path": "mid.md", "content": "z\ny\nx\n"})
		_, ok, err := r.Run(context.Background(), providerToolCall("write_file", raw))
		mu.Lock()
		writeOK = ok && err == nil
		mu.Unlock()
	}()
	close(gate2)
	wg2.Wait()

	if readOut.LinesRead != 3 {
		t.Errorf("scenario 3 read Outcome.LinesRead=%d, want 3 (read count preserved)", readOut.LinesRead)
	}
	if !writeOK {
		t.Fatal("scenario 3 write failed")
	}
	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("scenario 3: no edit record")
	}
	// read does not write the shared slot, so a blind write carries 0 here.
	if rec.ReadLines != 0 {
		t.Errorf("scenario 3: write stole read metadata ReadLines=%d, want 0", rec.ReadLines)
	}
}

// TestConcurrentReadWriteRaceFree (C#9): concurrent read→write cycles stay
// race-free under -race and each write still records a sane ReadLines.
// Each goroutine owns its own Registry (and its own shadow) so no shared
// mutable state exists across goroutines at all; the exercise is that the
// race detector stays quiet while these run in parallel.
func TestConcurrentReadWriteRaceFree(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			dir := t.TempDir()
			root, err := NewRoot(dir)
			if err != nil {
				t.Errorf("NewRoot: %v", err)
				return
			}
			sh, err := snap.New(root.Dir())
			if err != nil {
				t.Errorf("snap.New: %v", err)
				return
			}
			r := NewRegistry(root, sh)
			f := filepath.Join(dir, "f.md")
			if err := os.WriteFile(f, []byte("1\n2\n3\n4\n5\n"), 0o644); err != nil {
				t.Error(err)
				return
			}
			raw, _ := json.Marshal(map[string]any{"path": "f.md"})
			if _, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw)); err != nil || !ok {
				t.Errorf("read: %v", err)
				return
			}
			if rec := r.LastEdit(); rec != nil {
				t.Errorf("unexpected edit before write: %+v", rec)
			}
			rawW, _ := json.Marshal(map[string]any{"path": "f.md", "content": "new\n"})
			if _, ok, err := r.Run(context.Background(), providerToolCall("write_file", rawW)); err != nil || !ok {
				t.Errorf("write: %v", err)
				return
			}
			rec := r.LastEdit()
			if rec == nil {
				t.Errorf("write produced no edit record")
				return
			}
			if rec.ReadLines < 0 || rec.ReadLines > 5 {
				t.Errorf("ReadLines=%d out of range", rec.ReadLines)
			}
		}(i)
	}
	wg.Wait()
}

// TestRegistryInstancesIsolated (C#10): two Registries never share read
// metadata; a consume on one cannot affect the other. Reads are result-scoped
// (Outcome), and neither reads writes the other's write-slot.
func TestRegistryInstancesIsolated(t *testing.T) {
	r1, _ := newReg(t)
	r2, _ := newReg(t)

	f := filepath.Join(r1.root.Dir(), "x.md")
	os.WriteFile(f, []byte("1\n2\n3\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "x.md"})
	out, err := r1.RunDetailed(context.Background(), "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("r1 read: ok=%v err=%v", out.OK, err)
	}
	if out.LinesRead != 3 {
		t.Fatalf("r1 out.LinesRead=%d, want 3", out.LinesRead)
	}

	if got := r2.ConsumeLinesRead(); got != 0 {
		t.Fatalf("r2 saw r1's linesRead=%d, instances not isolated", got)
	}
	if got := r1.ConsumeLinesRead(); got != 0 {
		t.Fatalf("r1 read polluted its write-slot=%d, want 0 (result-scoped)", got)
	}
}

// TestReadFailureClearsViaRunDetailed (C#6 rich path): the RunDetailed path
// also clears on error so the loop's outcome never carries stale state.
func TestReadFailureClearsViaRunDetailed(t *testing.T) {
	r, _ := newReg(t)
	rawBad, _ := json.Marshal(map[string]any{"path": "missing.md"})
	out, err := r.RunDetailed(context.Background(), "read_file", rawBad)
	if err == nil {
		t.Fatal("expected error")
	}
	if out.Truncated {
		t.Fatal("failed read reported Truncated=true")
	}
	if out.NextOffset != 0 {
		t.Fatalf("failed read reported NextOffset=%d", out.NextOffset)
	}
	if got := r.ConsumeLinesRead(); got != 0 {
		t.Fatalf("failed read left linesRead=%d", got)
	}
}
