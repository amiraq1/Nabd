package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/ignorefile"
)

// TestWalkReadBatchIsBoundedByTheRemainingCeiling pins the read-ahead bound
// directly. The walk used to ask for a fixed batch of 64 entries even after the
// entry ceiling had been reached, so the last read allocated up to 64 DirEntry
// structs that were then discarded. The batch must shrink to what is still
// admissible, plus the single entry that proves truncation.
func TestWalkReadBatchIsBoundedByTheRemainingCeiling(t *testing.T) {
	cases := []struct {
		seen int
		want int
	}{
		{0, 64},
		{64, 64},
		{448, 64},
		{449, 64},
		{510, 3},
		{511, 2},
		{512, 1},
	}
	for _, tc := range cases {
		if got := readBatch(tc.seen); got != tc.want {
			t.Errorf("readBatch(%d) = %d, want %d", tc.seen, got, tc.want)
		}
	}
}

// TestWalkEntryCeilingBoundaryIsExact is a protection test: it pins the
// behaviour the bound must not change. A tree at the ceiling is listed in full
// with no truncation diagnostic; one entry past it is disclosed exactly once
// and the returned paths never exceed the ceiling.
func TestWalkEntryCeilingBoundaryIsExact(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxWalkEntries; i++ {
		name := filepath.Join(dir, fmt.Sprintf("s%04d.md", i))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	paths, diag := walk(dir, ignorefile.Matcher{})
	if len(paths) != maxWalkEntries {
		t.Fatalf("a tree at the ceiling listed %d paths, want %d", len(paths), maxWalkEntries)
	}
	if n := countTruncated(diag); n != 0 {
		t.Fatalf("a tree at the ceiling must not report truncation, got %d: %+v", n, diag)
	}

	over := filepath.Join(dir, "over.md")
	if err := os.WriteFile(over, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, diag = walk(dir, ignorefile.Matcher{})
	if len(paths) > maxWalkEntries {
		t.Fatalf("the returned paths exceeded the ceiling: %d", len(paths))
	}
	if n := countTruncated(diag); n != 1 {
		t.Fatalf("want exactly one truncation diagnostic, got %d: %+v", n, diag)
	}
}

func countTruncated(diag []Diagnostic) int {
	n := 0
	for _, d := range diag {
		if d.Kind == DiagTruncated {
			n++
		}
	}
	return n
}
