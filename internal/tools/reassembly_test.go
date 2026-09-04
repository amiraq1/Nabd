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

// TestThreeSegmentOffsetProgression: reads driven ONLY by the literal
// next_offset each read returns must march a 300-line fixture to EOF:
// strictly monotonic, no gap, no overlap, no duplicate range, and the
// final read reports truncated=false with no next_offset while the union
// covers exactly [1, total_lines]. The first segments' expectations are
// handwritten constants, not values derived from the code under test —
// a formula mirroring the implementation would pass any consistent
// (even wrong) scheme. This is the TRUNCATION_OFFSET_TEST gate.
func TestThreeSegmentOffsetProgression(t *testing.T) {
	// Pin the cap: maxReadBytes is a package var resolved from the env at
	// startup, and the handwritten constants below hold only at 3072.
	old := maxReadBytes
	maxReadBytes = 3072
	defer func() { maxReadBytes = old }()

	r, dir := newReg(t)
	path := filepath.Join(dir, "three.go")
	// Each line is exactly 200 bytes, so the 3072-byte cap yields 15
	// numbered lines per segment — arithmetic small enough to handwrite.
	var want strings.Builder
	for i := 1; i <= 300; i++ {
		prefix := "line-" + strconv.Itoa(i) + "|"
		pad := strings.Repeat("x", 200-len(prefix))
		want.WriteString(prefix + pad + "\n")
	}
	os.WriteFile(path, []byte(want.String()), 0o644)

	const totalLines = 300
	// Handwritten contract: 15 lines per segment at maxReadBytes=3072.
	// These constants are what distinguishes start+lines_read (16, 31, 46)
	// from the relative bug lines_read+1 (which would repeat 16 forever).
	wantNext := map[int]int{1: 16, 2: 31, 3: 46}
	wantRange := map[int][2]int{1: {1, 15}, 2: {16, 30}, 3: {31, 45}}

	type seg struct {
		start, end int
		nextOffset int
		truncated  bool
	}
	var segs []seg
	// The value entering round n+1 is the literal next_offset returned by
	// round n: nothing derived, nothing recomputed.
	offset := 1

	for round := 1; round <= 30; round++ {
		args := map[string]any{"path": "three.go"}
		if offset > 1 {
			args["offset"] = offset
		}
		raw, _ := json.Marshal(args)
		out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
		if err != nil || !ok {
			t.Fatalf("round %d: ok=%v err=%v", round, ok, err)
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
			t.Fatalf("round %d: no numbered lines in output", round)
		}
		segs = append(segs, seg{start: first, end: last, nextOffset: next, truncated: trunc})
		t.Logf("round %d: lines %d-%d, next_offset=%d, truncated=%v", round, first, last, next, trunc)

		// Handwritten gates on the first three segments: these exact
		// numbers are the absolute-offset contract.
		if n, ok := wantNext[round]; ok && next != n {
			t.Errorf("round %d: next_offset=%d, want handwritten %d", round, next, n)
		}
		if rng, ok := wantRange[round]; ok && (first != rng[0] || last != rng[1]) {
			t.Errorf("round %d: range %d-%d, want handwritten %d-%d", round, first, last, rng[0], rng[1])
		}
		if round == 1 {
			wantTail := "[TRUNCATED: read lines 1-15 of 300; continue with offset=16]"
			if !strings.Contains(out, wantTail) {
				got := "<no tail>"
				if idx := strings.Index(out, "[TRUNCATED"); idx >= 0 {
					got = out[idx:]
					if end := strings.Index(got, "]"); end >= 0 {
						got = got[:end+1]
					}
				}
				t.Errorf("round 1 tail = %q, want %q", got, wantTail)
			}
		}

		if !trunc {
			// EOF gates: the final read must be complete and silent about
			// offsets — an off-by-one here invites reading past the end.
			if next != 0 {
				t.Errorf("final read must not carry next_offset, got %d", next)
			}
			if strings.Contains(out, "[TRUNCATED") {
				t.Error("final read must not carry a truncation tail")
			}
			break
		}
		if next <= offset {
			t.Fatalf("round %d: next_offset=%d did not strictly advance past %d", round, next, offset)
		}
		offset = next
	}

	// Gate: the chain reached the end of the file.
	if len(segs) < 3 {
		t.Fatalf("need >= 3 segments, got %d (byte cap may not trigger enough)", len(segs))
	}
	last := segs[len(segs)-1]
	if last.truncated {
		t.Fatalf("read chain never completed: %d segments, still truncated at line %d", len(segs), last.end)
	}
	if segs[0].start != 1 {
		t.Errorf("first segment starts at %d, want 1", segs[0].start)
	}
	if last.end != totalLines {
		t.Errorf("final segment ends at %d, want %d — union must reach EOF", last.end, totalLines)
	}

	// Gate: union == [1, total] exactly — contiguous (each start equals
	// the previous next_offset), no duplicate range, total covered lines
	// exactly totalLines.
	type rng struct{ a, b int }
	seen := map[rng]bool{}
	covered := 0
	maxEnd := 0
	for i, s := range segs {
		r := rng{s.start, s.end}
		if seen[r] {
			t.Errorf("segment %d duplicate range %d-%d", i+1, r.a, r.b)
		}
		seen[r] = true
		covered += s.end - s.start + 1
		maxEnd = s.end
		// Gate: an offered offset must never point into already-read
		// territory — such an offer is an invitation to re-read.
		if s.truncated && s.nextOffset <= maxEnd {
			t.Errorf("segment %d offers offset=%d but lines up to %d are already read",
				i+1, s.nextOffset, maxEnd)
		}
		if i > 0 && s.start != segs[i-1].nextOffset {
			t.Errorf("segment %d start (%d) != previous next_offset (%d): gap or overlap",
				i+1, s.start, segs[i-1].nextOffset)
		}
	}
	if covered != totalLines {
		t.Errorf("union covers %d lines, want exactly %d", covered, totalLines)
	}

	t.Logf("RESULT: %d segments cover exactly [%d,%d]; final read complete",
		len(segs), segs[0].start, last.end)
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
