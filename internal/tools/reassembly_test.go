package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTruncationReassemblyExact: on a known 300-line fixture, a truncated
// read names the exact next_offset; reading with that offset returns the
// remainder; concatenating both reassembles the file with no gap and no
// duplicate — the STEP 2 closure criterion.
func TestTruncationReassemblyExact(t *testing.T) {
	r, dir := newReg(t)
	path := filepath.Join(dir, "known.go")
	// 300 lines, each "line-NNN" — short enough that only the byte cap
	// truncates, and easy to verify reassembly.
	var want strings.Builder
	for i := 1; i <= 300; i++ {
		want.WriteString("line-" + strconv.Itoa(i) + "\n")
	}
	os.WriteFile(path, []byte(want.String()), 0o644)

	// Read 1: from the start, expect truncation with explicit next_offset.
	raw, _ := json.Marshal(map[string]any{"path": "known.go"})
	out1, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if err != nil || !ok {
		t.Fatalf("read 1: ok=%v err=%v", ok, err)
	}
	trunc, next := r.ConsumeTruncated()
	if !trunc {
		t.Fatal("read 1 not truncated (fixture too small for the cap?)")
	}
	// next must equal the first line after the last line we saw. Content
	// lines are "N|line-M"; the N prefix is the actual line number.
	lastSeen := 0
	for _, ln := range strings.Split(out1, "\n") {
		if i := strings.Index(ln, "|"); i >= 0 {
			if n, e := strconv.Atoi(ln[:i]); e == nil && n > lastSeen {
				lastSeen = n
			}
		}
	}
	if next != lastSeen+1 {
		t.Fatalf("next_offset=%d, want %d (last seen line + 1)", next, lastSeen+1)
	}

	// Read 2: with the stated next_offset — must continue, no gap, no dup.
	raw2, _ := json.Marshal(map[string]any{"path": "known.go", "offset": next})
	out2, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw2))
	if err != nil || !ok {
		t.Fatalf("read 2: ok=%v err=%v", ok, err)
	}
	// The second read starts exactly at next.
	if !strings.HasPrefix(out2, strconv.Itoa(next)+"|") {
		t.Errorf("read 2 must start at line %d, got: %q", next, out2[:60])
	}

	// Reassemble: first line of read 2 must be lastSeen+1 (no gap/dup).
	// Verify by joining the numbered content.
	seen := map[int]bool{}
	for _, ln := range strings.Split(out1+"\n"+out2, "\n") {
		// content lines are "N|line-M" after truncTail changes? Check both.
		if i := strings.Index(ln, "|"); i >= 0 {
			if n, e := strconv.Atoi(ln[:i]); e == nil {
				seen[n] = true
			}
		}
	}
	// The union must be contiguous 1..N with no gaps.
	maxSeen := 0
	for n := range seen {
		if n > maxSeen {
			maxSeen = n
		}
	}
	for i := 1; i <= maxSeen; i++ {
		if !seen[i] {
			t.Errorf("gap at line %d — reassembly not exact", i)
		}
	}
	t.Logf("read1 covered through %d, next=%d, read2 covers %d..%d", lastSeen, next, next, maxSeen)
}

// TestThreeSegmentOffsetProgression: on a 300-line fixture, four consecutive
// reads (the first three truncated, the last complete) must produce a strictly
// monotonic next_offset chain with no overlap and no gap. This is the
// TRUNCATION_OFFSET_TEST gate: it proves the absolute-offset contract across
// three segments, not just two.
func TestThreeSegmentOffsetProgression(t *testing.T) {
	r, dir := newReg(t)
	path := filepath.Join(dir, "three.go")
	// Lines must be long enough that the byte cap (3072) reads only
	// ~10-15 lines per segment, forcing 3+ truncation rounds on a
	// 300-line fixture. Each line is ~200 chars ("line-NNN|" + padding).
	var want strings.Builder
	for i := 1; i <= 300; i++ {
		prefix := "line-" + strconv.Itoa(i) + "|"
		pad := strings.Repeat("x", 200-len(prefix))
		want.WriteString(prefix + pad + "\n")
	}
	os.WriteFile(path, []byte(want.String()), 0o644)

	type seg struct {
		start, end int
		nextOffset int
		truncated  bool
	}
	var segs []seg
	offset := 1

	for round := 0; round < 6; round++ {
		args := map[string]any{"path": "three.go"}
		if offset > 1 {
			args["offset"] = offset
		}
		raw, _ := json.Marshal(args)
		out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
		if err != nil || !ok {
			t.Fatalf("round %d: ok=%v err=%v", round+1, ok, err)
		}
		trunc, next := r.ConsumeTruncated()

		// Parse first and last numbered content lines.
		first, last := -1, -1
		for _, ln := range strings.Split(out, "\n") {
			if i := strings.Index(ln, "|"); i >= 0 {
				if n, e := strconv.Atoi(ln[:i]); e == nil {
					if first < 0 || n < first {
						first = n
					}
					if n > last {
						last = n
					}
				}
			}
		}
		if first < 0 {
			t.Fatalf("round %d: no numbered lines in output", round+1)
		}
		segs = append(segs, seg{start: first, end: last, nextOffset: next, truncated: trunc})
		t.Logf("round %d: lines %d-%d, next_offset=%d, truncated=%v", round+1, first, last, next, trunc)
		if !trunc {
			break
		}
		offset = next
	}

	// Gate: must have at least 3 truncated segments.
	if len(segs) < 3 {
		t.Fatalf("need >= 3 segments, got %d (byte cap may not trigger enough)", len(segs))
	}

	// Gate: next_offset strictly increases across truncated segments.
	for i := 1; i < len(segs); i++ {
		if segs[i].start <= segs[i-1].start {
			t.Errorf("segment %d start (%d) <= segment %d start (%d): not monotonic",
				i+1, segs[i].start, i, segs[i-1].start)
		}
	}

	// Gate: no gap or overlap — each segment starts where the previous
	// one's next_offset said it would.
	for i := 1; i < len(segs); i++ {
		if segs[i].start != segs[i-1].nextOffset {
			t.Errorf("segment %d start (%d) != segment %d next_offset (%d): gap or overlap",
				i+1, segs[i].start, i, segs[i-1].nextOffset)
		}
	}

	// Gate: no duplicate ranges.
	type rng struct{ a, b int }
	seen := map[rng]bool{}
	for i, s := range segs {
		r := rng{s.start, s.end}
		if seen[r] {
			t.Errorf("segment %d duplicate range %d-%d", i+1, r.a, r.b)
		}
		seen[r] = true
	}

	t.Logf("RESULT: %d segments, progression %d → %d → %d → %d",
		len(segs), segs[0].start, segs[1].start, segs[2].start, segs[len(segs)-1].start)
}

// TestNonTruncatedNoNextOffset: a small file read fully must not carry a
// truncation tail nor set the flag/offset.
func TestNonTruncatedNoNextOffset(t *testing.T) {
	r, dir := newReg(t)
	path := filepath.Join(dir, "small.md")
	os.WriteFile(path, []byte("سطر واحد\nسطر اثنان\n"), 0o644)

	raw, _ := json.Marshal(map[string]any{"path": "small.md"})
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if err != nil || !ok {
		t.Fatalf("read: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "next_offset=") {
		t.Errorf("non-truncated read must not carry next_offset: %q", out)
	}
	if trunc, next := r.ConsumeTruncated(); trunc || next != 0 {
		t.Errorf("non-truncated read set flag=%v offset=%d", trunc, next)
	}
}
