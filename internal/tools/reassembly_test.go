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
