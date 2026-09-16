// These tests are untagged on purpose. The measurement files in this package
// carry //go:build measure, so CI never compiles them; the contracts that must
// not regress silently -- containment, the shadow-store exclusion, the resolve
// budget and the reachability of each limit -- belong where go test can see them.
//
// Helper and test names here must not collide with the measure-tagged files,
// because a run with -tags measure compiles both sets into one package.
package pathindex

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"nabd/internal/tools"
)

// writeTree builds dirs directories, each holding filesPerDir regular files.
func writeTree(t *testing.T, dirs, filesPerDir int) *tools.Root {
	t.Helper()
	base := t.TempDir()
	for d := 0; d < dirs; d++ {
		sub := filepath.Join(base, fmt.Sprintf("d%02d", d))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < filesPerDir; f++ {
			if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%02d.go", f)), []byte("package x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	root, err := tools.NewRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func newRootAt(t *testing.T, dir string) *tools.Root {
	t.Helper()
	root, err := tools.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestDefaultCandidateLimitIsReachable is the structural guard the measure files
// cannot provide to CI: every candidate is also an entry, so equal limits make
// the candidate limit dead code.
func TestDefaultCandidateLimitIsReachable(t *testing.T) {
	if DefaultMaxCandidates >= DefaultMaxEntries {
		t.Fatalf("DefaultMaxCandidates (%d) >= DefaultMaxEntries (%d): every candidate is also an entry, so the candidate limit can never trip",
			DefaultMaxCandidates, DefaultMaxEntries)
	}
}

func TestScanListsRegularFilesRelativeToRoot(t *testing.T) {
	idx := Scan(writeTree(t, 2, 2), Config{})
	if !idx.Complete() {
		t.Fatalf("Stop = %q, want a completed scan", idx.Stop)
	}
	want := []string{"d00/f00.go", "d00/f01.go", "d01/f00.go", "d01/f01.go"}
	if len(idx.Paths) != len(want) {
		t.Fatalf("Paths = %v, want %v", idx.Paths, want)
	}
	for i := range want {
		if idx.Paths[i] != want[i] {
			t.Fatalf("Paths = %v, want %v (breadth-first, each directory sorted)", idx.Paths, want)
		}
	}
	if idx.Candidates != len(want) {
		t.Fatalf("Candidates = %d, want %d", idx.Candidates, len(want))
	}
}

func TestScanIsReproducible(t *testing.T) {
	root := writeTree(t, 3, 3)
	first := Scan(root, Config{})
	second := Scan(root, Config{})
	if len(first.Paths) != len(second.Paths) {
		t.Fatalf("lengths differ: %d vs %d", len(first.Paths), len(second.Paths))
	}
	for i := range first.Paths {
		if first.Paths[i] != second.Paths[i] {
			t.Fatalf("order differs at %d: %q vs %q; the index must be reproducible", i, first.Paths[i], second.Paths[i])
		}
	}
}

// TestScanSkipsExcludedDirectories covers the containment half of the exclusion
// list as well as the performance half: .ag holds the shadow history and must
// never be offered back as a project path.
func TestScanSkipsExcludedDirectories(t *testing.T) {
	base := t.TempDir()
	for _, dir := range []string{"src", ".git", ".ag", "node_modules", "dist"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, dir, "a.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx := Scan(newRootAt(t, base), Config{})
	if len(idx.Paths) != 1 || idx.Paths[0] != "src/a.go" {
		t.Fatalf("Paths = %v, want only [src/a.go]", idx.Paths)
	}
}

// TestScanRefusesSymlinkedEntries is the containment contract: ReadDir reports a
// symlink as a symlink, so no link -- inside the root or pointing out of it --
// can put a path into the index.
func TestScanRefusesSymlinkedEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on windows")
	}
	base := t.TempDir()
	target := filepath.Join(base, "real.go")
	if err := os.WriteFile(target, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, "inside.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "escape.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	idx := Scan(newRootAt(t, base), Config{})
	if len(idx.Paths) != 1 || idx.Paths[0] != "real.go" {
		t.Fatalf("Paths = %v, want only [real.go]: a symlinked entry must never enter the index", idx.Paths)
	}
	if idx.Rejected != 2 {
		t.Fatalf("Rejected = %d, want 2 (both links)", idx.Rejected)
	}
}

// TestScanResolvesOncePerDirectory is the cost contract. If it fails, a
// per-candidate Resolve has come back and the scan is O(files) syscalls again.
func TestScanResolvesOncePerDirectory(t *testing.T) {
	const dirs, files = 6, 5
	idx := Scan(writeTree(t, dirs, files), Config{})
	if idx.Candidates != dirs*files {
		t.Fatalf("Candidates = %d, want %d", idx.Candidates, dirs*files)
	}
	if idx.ResolveCalls != dirs+1 {
		t.Fatalf("ResolveCalls = %d, want one per directory including the root (%d)", idx.ResolveCalls, dirs+1)
	}
	if idx.ResolveCalls >= idx.Candidates {
		t.Fatalf("ResolveCalls (%d) reached the candidate count (%d): the per-candidate policy is back", idx.ResolveCalls, idx.Candidates)
	}
	if idx.ResolveCalls != idx.DirsVisited {
		t.Fatalf("ResolveCalls = %d, DirsVisited = %d; they must agree when no directory is refused", idx.ResolveCalls, idx.DirsVisited)
	}
}

func TestScanEntryLimitStopsTheWalk(t *testing.T) {
	idx := Scan(writeTree(t, 5, 5), Config{MaxEntries: 7})
	if idx.Stop != StopEntries {
		t.Fatalf("Stop = %q, want %q", idx.Stop, StopEntries)
	}
	if idx.Entries != 7 {
		t.Fatalf("Entries = %d, want the limit 7", idx.Entries)
	}
	if idx.Complete() {
		t.Fatal("Complete must be false when the entry limit trips")
	}
}

func TestScanCandidateLimitStopsTheWalk(t *testing.T) {
	idx := Scan(writeTree(t, 5, 5), Config{MaxCandidates: 3})
	if idx.Stop != StopCandidates {
		t.Fatalf("Stop = %q, want %q", idx.Stop, StopCandidates)
	}
	if idx.Candidates != 3 || len(idx.Paths) != 3 {
		t.Fatalf("Candidates = %d, len(Paths) = %d, want 3 and 3", idx.Candidates, len(idx.Paths))
	}
	if idx.Complete() {
		t.Fatal("Complete must be false when the candidate limit trips")
	}
}

func TestScanTimeoutStopsTheWalk(t *testing.T) {
	// A clock that advances one second per reading: no sleep, no flakiness.
	tick := time.Unix(0, 0)
	idx := Scan(writeTree(t, 5, 5), Config{
		Timeout: 3 * time.Second,
		now: func() time.Time {
			tick = tick.Add(time.Second)
			return tick
		},
	})
	if idx.Stop != StopTimeout {
		t.Fatalf("Stop = %q, want %q", idx.Stop, StopTimeout)
	}
	if idx.Complete() {
		t.Fatal("Complete must be false when the timeout trips")
	}
}

// TestDefaultTimeoutDoesNotBindBeforeTheCandidateLimit keeps the Phase 0
// measurement under CI's eye. DefaultTimeout is documented as a safety valve,
// not a budget, and the measure-tagged files that produced that number are never
// compiled here. This test walks a tree wider than the candidate limit under the
// production defaults and pins the conclusion: the walk stops on the counter,
// not on the clock, with room to spare.
func TestDefaultTimeoutDoesNotBindBeforeTheCandidateLimit(t *testing.T) {
	// More candidates than DefaultMaxCandidates, so StopCandidates is the only
	// stop the counter can explain.
	const dirs, filesPerDir = 600, 20 // 12,000 candidates
	idx := Scan(writeTree(t, dirs, filesPerDir), Config{})

	if idx.Stop == StopTimeout {
		t.Fatalf("the %v cap stopped the walk after %v at %d candidates: the cap is the binding limit, not a safety valve",
			DefaultTimeout, idx.Elapsed.Round(time.Millisecond), idx.Candidates)
	}
	if idx.Stop != StopCandidates {
		t.Fatalf("Stop = %q, want %q: this tree must stop on the candidate limit", idx.Stop, StopCandidates)
	}
	t.Logf("%d candidates in %v against a %v cap (%.0fx margin)",
		idx.Candidates, idx.Elapsed.Round(time.Millisecond), DefaultTimeout,
		float64(DefaultTimeout)/float64(idx.Elapsed))
	if idx.Elapsed >= DefaultTimeout/2 {
		t.Fatalf("reaching %d candidates took %v, at or above half the %v cap: the margin the cap claims is gone",
			idx.Candidates, idx.Elapsed.Round(time.Millisecond), DefaultTimeout)
	}
}

func TestScanCompletesUnderEveryLimit(t *testing.T) {
	idx := Scan(writeTree(t, 4, 4), Config{})
	if idx.Stop != StopComplete || !idx.Complete() {
		t.Fatalf("Stop = %q, Complete = %v; want a completed scan", idx.Stop, idx.Complete())
	}
	if idx.Candidates != 16 {
		t.Fatalf("Candidates = %d, want 16", idx.Candidates)
	}
	if idx.Rejected != 0 {
		t.Fatalf("Rejected = %d, want 0", idx.Rejected)
	}
}
