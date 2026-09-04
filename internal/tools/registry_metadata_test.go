package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// TestReadReportsOwnLineCount (C#1): read_file records its own line count.
func TestReadReportsOwnLineCount(t *testing.T) {
	r, dir := newReg(t)
	f := filepath.Join(dir, "five.md")
	os.WriteFile(f, []byte("1\n2\n3\n4\n5\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "five.md"})
	if _, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw)); err != nil || !ok {
		t.Fatalf("read_file: ok=%v err=%v", ok, err)
	}
	if got := r.ConsumeLinesRead(); got != 5 {
		t.Fatalf("ConsumeLinesRead=%d, want 5", got)
	}
	if got := r.ConsumeLinesRead(); got != 0 {
		t.Fatalf("second consume=%d, want 0 (consumed once)", got)
	}
}

// TestSubsequentWriteReportsZeroReadLines (C#2): after a read is consumed by
// one write, a later blind write must carry 0 — the count cannot survive to
// an unrelated later operation.
func TestSubsequentWriteReportsZeroReadLines(t *testing.T) {
	r, _ := newReg(t)
	// read 3 lines
	f := filepath.Join(r.root.Dir(), "a.md")
	os.WriteFile(f, []byte("a\nb\nc\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "a.md"})
	if _, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw)); err != nil || !ok {
		t.Fatalf("read_file: ok=%v err=%v", ok, err)
	}
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
// must carry 5 (the latest), not 8.
func TestTwoSequentialReadsDoNotAccumulate(t *testing.T) {
	r, dir := newReg(t)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("1\n2\n3\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("1\n2\n3\n4\n5\n"), 0o644)
	for _, p := range []string{"a.md", "b.md"} {
		raw, _ := json.Marshal(map[string]any{"path": p})
		if _, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw)); err != nil || !ok {
			t.Fatalf("read %s: ok=%v err=%v", p, ok, err)
		}
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

// TestFailedReadDoesNotContaminate (C#6): a read that errors must leave no
// metadata for the next action.
func TestFailedReadDoesNotContaminate(t *testing.T) {
	r, _ := newReg(t)
	// set a pending count first
	f := filepath.Join(r.root.Dir(), "ok.md")
	os.WriteFile(f, []byte("1\n2\n3\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "ok.md"})
	if _, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw)); err != nil || !ok {
		t.Fatalf("read_file: ok=%v err=%v", ok, err)
	}

	// failed read (missing file) must clear pending state
	rawBad, _ := json.Marshal(map[string]any{"path": "nope.md"})
	_, _, err := r.Run(context.Background(), providerToolCall("read_file", rawBad))
	if err == nil {
		t.Fatal("expected error reading missing file")
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

// TestConcurrentReadsMetadataIsolated (C#8): concurrent reads through one
// Registry exercise the Consume contract under contention. The slot is
// shared, so consumers race with one another — the invariants that must
// hold are: every observed value is a whole 3 or 0 (never torn or garbage),
// at least one consumer actually takes the recorded value, a second consume
// always returns 0 (ownership is one-shot), and after all goroutines finish
// no state survives. The test's main job is staying quiet under -race.
func TestConcurrentReadsMetadataIsolated(t *testing.T) {
	r, dir := newReg(t)
	f := filepath.Join(dir, "n.md")
	os.WriteFile(f, []byte("1\n2\n3\n"), 0o644)

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		seens  int // goroutines whose first consume returned 3
		zeroes int // goroutines whose first consume returned 0
		bad    []string
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{"path": "n.md"})
			if _, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw)); err != nil || !ok {
				t.Errorf("read_file: ok=%v err=%v", ok, err)
				return
			}
			first := r.ConsumeLinesRead()
			second := r.ConsumeLinesRead()
			mu.Lock()
			defer mu.Unlock()
			switch first {
			case 3:
				seens++
			case 0:
				zeroes++
			default:
				bad = append(bad, fmt.Sprintf("first consume=%d", first))
			}
			if second != 0 {
				bad = append(bad, fmt.Sprintf("second consume=%d, want 0", second))
			}
		}()
	}
	wg.Wait()
	if len(bad) > 0 {
		t.Fatalf("consume contract violated: %v", bad)
	}
	if seens == 0 {
		t.Fatalf("no consumer ever observed the recorded value (seens=0, zeroes=%d)", zeroes)
	}
	if got := r.ConsumeLinesRead(); got != 0 {
		t.Fatalf("final consume=%d, want 0 (no state survived the goroutines)", got)
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
// metadata; a consume on one cannot affect the other.
func TestRegistryInstancesIsolated(t *testing.T) {
	r1, _ := newReg(t)
	r2, _ := newReg(t)

	f := filepath.Join(r1.root.Dir(), "x.md")
	os.WriteFile(f, []byte("1\n2\n3\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"path": "x.md"})
	if _, ok, err := r1.Run(context.Background(), providerToolCall("read_file", raw)); err != nil || !ok {
		t.Fatalf("r1 read: %v", err)
	}

	if got := r2.ConsumeLinesRead(); got != 0 {
		t.Fatalf("r2 saw r1's linesRead=%d, instances not isolated", got)
	}
	if got := r1.ConsumeLinesRead(); got != 3 {
		t.Fatalf("r1 ConsumeLinesRead=%d, want 3", got)
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
