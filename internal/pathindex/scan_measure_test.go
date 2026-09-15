//go:build measure

// Phase 0 of the @-picker: measure before choosing a number, under the approved
// policy. This file carries no production code. It holds a prototype of the
// Phase 1 scan plus the deterministic tests for its stop conditions, so the entry,
// candidate and wall-clock limits are chosen from numbers taken on the machine
// the feature runs on.
//
// The policy measured here is the approved one: Root.Resolve is called once per
// visited directory, never once per candidate. A candidate is classified with
// entry.Type().IsRegular() and joined to its resolved directory; a symlink leaf is
// refused by ModeSymlink without a second syscall.
//
// Run it with: go test -tags measure ./internal/pathindex/ -run 'TestScan|TestMeasure' -v
//
// NABD_MEASURE_ROOT selects the tree for the measurement tests (default: the module
// root). NABD_MEASURE_CUTOFF (seconds) bounds one uncapped run.
package pathindex

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"nabd/internal/tools"
)

// exclusions is the set of directory names the walk never enters.
type exclusions map[string]bool

// literalV0 is the policy exactly as v0 words it: .git and .ag, nothing else.
// .gitignore handling is deferred to v1, and this policy says so by construction
// rather than by claiming to respect it.
var literalV0 = exclusions{".git": true, ".ag": true}

// shortListV0 is that policy plus the list the repository already uses to keep
// walkers out of generated trees (internal/tools/registry.go: skipDir).
var shortListV0 = exclusions{
	".git": true, ".ag": true, "node_modules": true, "vendor": true,
	".venv": true, "__pycache__": true, "target": true, "dist": true,
	"build": true, ".next": true, ".cache": true, ".idea": true,
}

// Approved limits. The scan stops at the first one that trips.
const (
	// limitEntries must stay above limitCandidates: every candidate is also an
	// entry, so equal limits make the candidate limit unreachable in production.
	limitEntries    = 50000
	limitCandidates = 10000
	limitTimeout    = 2 * time.Second // safety valve; calibrated by TestMeasure*
)

type stopReason string

const (
	stopComplete   stopReason = "complete"
	stopEntries    stopReason = "entry-limit"
	stopCandidates stopReason = "candidate-limit"
	stopTimeout    stopReason = "timeout"
)

type scanConfig struct {
	maxEntries    int
	maxCandidates int
	timeout       time.Duration
	excluded      exclusions
	now           func() time.Time // injected so limit tests need no sleep
}

func (c scanConfig) clock() func() time.Time {
	if c.now != nil {
		return c.now
	}
	return time.Now
}

type milestone struct {
	candidates int
	entries    int
	elapsed    time.Duration
}

type dirCount struct {
	rel     string
	entries int
}

type scanResult struct {
	entries      int
	dirsVisited  int
	dirsAccepted int
	dirsRejected int
	candidates   int
	rejected     int

	resolveCalls int
	resolveTime  time.Duration
	elapsed      time.Duration

	stop       stopReason
	maxDepth   int
	milestones []milestone
	walls      []dirCount
}

func (r scanResult) complete() bool { return r.stop == stopComplete }

// scan is the prototype of the Phase 1 Scan: same traversal and same policy, with
// counters attached. Resolve is called once per directory; the candidate loop makes
// no syscall beyond the ReadDir that produced the entry.
func scan(root *tools.Root, cfg scanConfig) scanResult {
	var s scanResult
	now := cfg.clock()
	start := now()

	type item struct {
		rel   string
		depth int
	}
	queue := []item{{rel: ".", depth: 0}}

	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]

		// One resolution per directory, not per candidate.
		t0 := now()
		abs, err := root.Resolve(dir.rel)
		s.resolveTime += now().Sub(t0)
		s.resolveCalls++
		if err != nil {
			s.dirsRejected++
			s.rejected++
			continue
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			s.dirsRejected++
			s.rejected++
			continue
		}
		s.dirsAccepted++
		if len(entries) >= 3 {
			s.walls = append(s.walls, dirCount{rel: dir.rel, entries: len(entries)})
		}
		// Sorted entries plus a FIFO queue keeps the walk deterministic when it
		// completes, which is what makes the index reproducible.
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		s.dirsVisited++
		if dir.depth > s.maxDepth {
			s.maxDepth = dir.depth
		}

		for _, e := range entries {
			if s.entries >= cfg.maxEntries {
				s.stop = stopEntries
				s.elapsed = now().Sub(start)
				return s
			}
			if now().Sub(start) > cfg.timeout {
				s.stop = stopTimeout
				s.elapsed = now().Sub(start)
				return s
			}
			s.entries++

			name := e.Name()
			rel := name
			if dir.rel != "." {
				rel = path.Join(dir.rel, name)
			}

			// ReadDir never follows a link, so a symlink arrives here as a
			// symlink: refused with no second syscall and no TOCTOU window.
			if e.Type()&fs.ModeSymlink != 0 {
				s.rejected++
				continue
			}
			if e.IsDir() {
				if !cfg.excluded[name] {
					queue = append(queue, item{rel: rel, depth: dir.depth + 1})
				}
				continue
			}
			// Classification is the DirEntry's own type: no os.Stat per candidate.
			if !e.Type().IsRegular() {
				s.rejected++
				continue
			}
			if s.candidates >= cfg.maxCandidates {
				s.stop = stopCandidates
				s.elapsed = now().Sub(start)
				return s
			}
			s.candidates++
			s.milestones = recordMilestones(s.milestones, s.candidates, s.entries, now().Sub(start))
		}
	}
	s.stop = stopComplete
	s.elapsed = now().Sub(start)
	return s
}

// measureMilestones are the candidate counts whose elapsed time is recorded.
var measureMilestones = []int{100, 500, 1000, 2000, 5000, 10000, 25000, 50000}

func recordMilestones(got []milestone, candidates, entries int, elapsed time.Duration) []milestone {
	for _, m := range measureMilestones {
		if candidates == m {
			got = append(got, milestone{candidates: m, entries: entries, elapsed: elapsed})
		}
	}
	return got
}

// envLine describes where a number came from. Every result table carries one.
func envLine() string {
	return fmt.Sprintf("%s · %s/%s · NumCPU=%d · warm-cache measurement; drop_caches is unavailable in this environment",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}

func repoRootFromEnv(t *testing.T) string {
	t.Helper()
	if r := os.Getenv("NABD_MEASURE_ROOT"); r != "" {
		return r
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory; set NABD_MEASURE_ROOT")
		}
		dir = parent
	}
}

func measureCutoff() time.Duration {
	if s := os.Getenv("NABD_MEASURE_CUTOFF"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 30 * time.Second
}

func measureRounds() int {
	if s := os.Getenv("NABD_MEASURE_ROUNDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 2
}

func percent(part, whole time.Duration) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}

func firstN(in []dirCount, n int) []dirCount {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

// reportOne prints a result in the shape the report needs: which limit tripped
// first, how many entries were inspected, how many candidates were kept, the time
// to the stop, and why it stopped.
func reportOne(label string, s scanResult, cfg scanConfig) {
	complete := "true"
	if !s.complete() {
		complete = "false (" + string(s.stop) + ")"
	}
	fmt.Printf("   %-26s entries=%-6d candidates=%-6d dirs=%-5d resolve=%-5d (%v, %.0f%%) elapsed=%-8v Complete=%s\n",
		label, s.entries, s.candidates, s.dirsAccepted, s.resolveCalls,
		s.resolveTime.Round(time.Microsecond), percent(s.resolveTime, s.elapsed),
		s.elapsed.Round(time.Microsecond), complete)
	if s.dirsRejected > 0 || s.rejected > 0 {
		fmt.Printf("   %-26s rejected: dirs=%d entries=%d\n", "", s.dirsRejected, s.rejected)
	}
	for _, m := range s.milestones {
		fmt.Printf("      %6d candidates at %8v (%d entries)\n", m.candidates, m.elapsed.Round(time.Microsecond), m.entries)
	}
	if len(s.walls) > 0 {
		sort.Slice(s.walls, func(i, j int) bool {
			if s.walls[i].entries != s.walls[j].entries {
				return s.walls[i].entries > s.walls[j].entries
			}
			return s.walls[i].rel < s.walls[j].rel
		})
		fmt.Printf("      widest directories: ")
		for _, d := range firstN(s.walls, 5) {
			fmt.Printf("%s=%d ", d.rel, d.entries)
		}
		fmt.Println()
	}
}

// ─── deterministic tests: the stop conditions, with an injected clock ─────────

// buildTree creates root/dirs dirs, each holding filesPerDir regular files.
func buildTree(t *testing.T, dirs, filesPerDir int) *tools.Root {
	t.Helper()
	base := t.TempDir()
	for d := 0; d < dirs; d++ {
		sub := filepath.Join(base, fmt.Sprintf("d%03d", d))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < filesPerDir; f++ {
			if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%03d.go", f)), []byte("package x\n"), 0o644); err != nil {
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

func TestScanStopsAtEntryLimit(t *testing.T) {
	root := buildTree(t, 20, 20) // 400 files over 20 dirs
	s := scan(root, scanConfig{maxEntries: 10, maxCandidates: 1000, timeout: time.Minute, excluded: literalV0})
	if s.stop != stopEntries {
		t.Fatalf("stop = %q, want %q", s.stop, stopEntries)
	}
	if s.entries != 10 {
		t.Fatalf("entries = %d, want the limit 10", s.entries)
	}
	if s.complete() {
		t.Fatal("Complete must be false when an entry limit trips")
	}
}

func TestScanStopsAtCandidateLimit(t *testing.T) {
	root := buildTree(t, 20, 20)
	s := scan(root, scanConfig{maxEntries: 1000, maxCandidates: 5, timeout: time.Minute, excluded: literalV0})
	if s.stop != stopCandidates {
		t.Fatalf("stop = %q, want %q", s.stop, stopCandidates)
	}
	if s.candidates != 5 {
		t.Fatalf("candidates = %d, want the limit 5", s.candidates)
	}
	if s.complete() {
		t.Fatal("Complete must be false when a candidate limit trips")
	}
	// The tree is wide and flat, so the candidate limit is what trips first.
	if s.entries < s.candidates {
		t.Fatalf("entries %d < candidates %d", s.entries, s.candidates)
	}
}

// TestCandidateLimitIsReachableUnderProductionLimits is the structural guard for the
// two counters. The test above exercises the candidate path with a small maxCandidates;
// this one keeps the shipped constants from making that path unreachable: entries
// counts every directory, symlink and file, while candidates counts only regular
// files, so candidates <= entries always holds and the candidate limit can only trip
// when limitEntries is strictly larger.
func TestCandidateLimitIsReachableUnderProductionLimits(t *testing.T) {
	if limitCandidates >= limitEntries {
		t.Fatalf("limitCandidates (%d) >= limitEntries (%d): every candidate is also an entry, so the candidate limit can never trip",
			limitCandidates, limitEntries)
	}
}

func TestScanStopsAtTimeout(t *testing.T) {
	root := buildTree(t, 20, 20)
	// A clock that advances one second per reading: no sleep, no flakiness.
	tick := time.Unix(0, 0)
	fake := func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
	s := scan(root, scanConfig{maxEntries: 10000, maxCandidates: 10000, timeout: 3 * time.Second, excluded: literalV0, now: fake})
	if s.stop != stopTimeout {
		t.Fatalf("stop = %q, want %q", s.stop, stopTimeout)
	}
	if s.complete() {
		t.Fatal("Complete must be false when the timeout trips")
	}
}

// TestScanResolvesPerDirectoryNotPerCandidate is the guard for the policy: the
// Resolve count is the number of directories, not the number of candidates. The
// tree is built wide and flat so the two numbers cannot be confused.
func TestScanResolvesPerDirectoryNotPerCandidate(t *testing.T) {
	const dirs, files = 12, 8
	root := buildTree(t, dirs, files)
	s := scan(root, scanConfig{maxEntries: 10000, maxCandidates: 10000, timeout: time.Minute, excluded: literalV0})

	wantCandidates := dirs * files
	if s.candidates != wantCandidates {
		t.Fatalf("candidates = %d, want %d", s.candidates, wantCandidates)
	}
	// The temporary root itself plus one directory per subdirectory.
	wantDirs := dirs + 1
	if s.resolveCalls != wantDirs {
		t.Fatalf("Resolve calls = %d, want one per directory (%d)", s.resolveCalls, wantDirs)
	}
	if s.resolveCalls >= s.candidates {
		t.Fatalf("Resolve calls (%d) reached the candidate count (%d): per-candidate policy is back", s.resolveCalls, s.candidates)
	}
	if s.resolveCalls != s.dirsAccepted+s.dirsRejected {
		t.Fatalf("Resolve calls = %d, want dirsAccepted+dirsRejected = %d", s.resolveCalls, s.dirsAccepted+s.dirsRejected)
	}
}

func TestScanCompleteWhenUnderEveryLimit(t *testing.T) {
	root := buildTree(t, 5, 5)
	s := scan(root, scanConfig{maxEntries: 10000, maxCandidates: 10000, timeout: time.Minute, excluded: literalV0})
	if s.stop != stopComplete || !s.complete() {
		t.Fatalf("stop = %q, complete = %v; want a completed scan", s.stop, s.complete())
	}
	if s.candidates != 25 {
		t.Fatalf("candidates = %d, want 25", s.candidates)
	}
}

// ─── measurements ────────────────────────────────────────────────────────────

// TestMeasureRepoRoot measures the tree this repository lives in, under the
// approved policy, uncapped and then under the production limits.
func TestMeasureRepoRoot(t *testing.T) {
	root, err := tools.NewRoot(repoRootFromEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\nenvironment: %s\nmethod: %d uncapped rounds per policy, then one capped run\nroot: %s\n",
		envLine(), measureRounds(), root.Dir())

	for _, p := range []struct {
		name string
		ex   exclusions
	}{{"literal v0 (.git,.ag)", literalV0}, {"v0 + skipDir list", shortListV0}} {
		fmt.Printf("\n== %s ==\n", p.name)
		first, best := time.Duration(0), time.Duration(0)
		for round := 0; round < measureRounds(); round++ {
			s := scan(root, scanConfig{maxEntries: 1 << 30, maxCandidates: 1 << 30, timeout: measureCutoff(), excluded: p.ex})
			if round == 0 {
				first = s.elapsed
			} else if best == 0 || s.elapsed < best {
				best = s.elapsed
			}
			reportOne(fmt.Sprintf("round %d", round+1), s, scanConfig{})
		}
		fmt.Printf("   first-in-process=%v best-of-rest=%v\n", first.Round(time.Microsecond), best.Round(time.Microsecond))

		s := scan(root, scanConfig{maxEntries: limitEntries, maxCandidates: limitCandidates, timeout: limitTimeout, excluded: p.ex})
		reportOne("production limits", s, scanConfig{})
	}
}

// TestMeasureEntryLimit measures the run that trips the entry limit first: a tree
// whose entries are mostly directories or refused entries, so 50,000 entries are
// inspected while far fewer candidates accumulate.
func TestMeasureEntryLimit(t *testing.T) {
	root, err := tools.NewRoot(repoRootFromEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\nenvironment: %s\nmethod: %d rounds at the production limits; the entry limit is the binding one\nroot: %s\n",
		envLine(), measureRounds(), root.Dir())
	for round := 0; round < measureRounds(); round++ {
		s := scan(root, scanConfig{maxEntries: limitEntries, maxCandidates: limitCandidates, timeout: measureCutoff(), excluded: shortListV0})
		reportOne(fmt.Sprintf("round %d", round+1), s, scanConfig{})
	}
}

// TestMeasureCandidateLimitWideTree measures the other binding case on a tree built
// for it: dirs x files regular files, no subdirectories of interest, so almost
// every entry is a candidate and the candidate limit trips before the entry limit.
func TestMeasureCandidateLimitWideTree(t *testing.T) {
	const dirs, files = 1200, 10 // 12,000 candidates in 13,200 entries

	base := t.TempDir()
	for d := 0; d < dirs; d++ {
		sub := filepath.Join(base, fmt.Sprintf("pkg%03d", d))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < files; f++ {
			if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("file%03d.go", f)), []byte("package p\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	root, err := tools.NewRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\nenvironment: %s\nmethod: %d rounds at the production limits on a synthetic %dx%d tree (almost every entry is a candidate)\nroot: %s\n",
		envLine(), measureRounds(), dirs, files, root.Dir())

	for round := 0; round < measureRounds(); round++ {
		s := scan(root, scanConfig{maxEntries: limitEntries, maxCandidates: limitCandidates, timeout: measureCutoff(), excluded: shortListV0})
		if s.stop != stopCandidates {
			t.Fatalf("round %d: stop = %q, want %q (candidates=%d entries=%d)",
				round+1, s.stop, stopCandidates, s.candidates, s.entries)
		}
		reportOne(fmt.Sprintf("round %d", round+1), s, scanConfig{})
	}
}

// TestMeasureEntryLimitDirHeavy measures the run that trips the entry limit first.
// The trees measured above are file-dense, so the candidate limit stops them well
// before the entry limit; a directory-heavy tree (many nested directories, one file
// per directory) is what makes 50,000 entries bind, and it is the shape a monorepo
// has. The candidate count stays at one per parent, well under the candidate limit.
func TestMeasureEntryLimitDirHeavy(t *testing.T) {
	const parents, subsPerParent = 5000, 9

	base := t.TempDir()
	for p := 0; p < parents; p++ {
		parent := filepath.Join(base, fmt.Sprintf("m%04d", p))
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, "leaf.go"), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for s := 0; s < subsPerParent; s++ {
			if err := os.MkdirAll(filepath.Join(parent, fmt.Sprintf("s%02d", s)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	root, err := tools.NewRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\nenvironment: %s\nmethod: %d rounds at the production limits on a synthetic directory-heavy tree (%d parents x %d subdirs + 1 file => ~%d entries, %d candidates)\nroot: %s\n",
		envLine(), measureRounds(), parents, subsPerParent, parents*(subsPerParent+1), parents, root.Dir())

	for round := 0; round < measureRounds(); round++ {
		s := scan(root, scanConfig{maxEntries: limitEntries, maxCandidates: limitCandidates, timeout: measureCutoff(), excluded: shortListV0})
		reportOne(fmt.Sprintf("round %d", round+1), s, scanConfig{})
	}
}

// TestMeasureResolveCallsCounts logs the counter that proves the policy on every
// measured tree, so the number is read from the counter rather than inferred from
// the share of wall time.
func TestMeasureResolveCallsCounts(t *testing.T) {
	root, err := tools.NewRoot(repoRootFromEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\nenvironment: %s\nResolve counter check (dirs vs candidates):\n", envLine())
	for _, p := range []struct {
		name string
		ex   exclusions
	}{{"literal v0 (.git,.ag)", literalV0}, {"v0 + skipDir list", shortListV0}} {
		s := scan(root, scanConfig{maxEntries: 1 << 30, maxCandidates: 1 << 30, timeout: measureCutoff(), excluded: p.ex})
		fmt.Printf("   %-22s resolveCalls=%-5d dirsAccepted=%-5d dirsRejected=%-3d candidates=%-5d | calls==dirs? %v | calls<candidates? %v\n",
			p.name, s.resolveCalls, s.dirsAccepted, s.dirsRejected, s.candidates,
			s.resolveCalls == s.dirsAccepted+s.dirsRejected, s.resolveCalls < s.candidates)
	}
}
