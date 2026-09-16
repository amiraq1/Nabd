// Package pathindex walks the project tree once and returns the regular files
// inside it. It is the index the @-picker completes against.
//
// The policy here is the one measured in internal/pathindex/scan_measure_test.go
// and internal/pathindex/scan_timeout_measure_test.go, promoted unchanged:
//
//   - Root.Resolve is called once per visited directory, never once per
//     candidate. Containment is decided for the directory, and its entries
//     inherit that decision through the ReadDir that produced them.
//   - A candidate is classified with the DirEntry's own type. There is no
//     os.Stat per candidate, so a scan of n files costs n/fanout resolutions
//     rather than n.
//   - ReadDir never follows a link, so a symlink arrives here as a symlink and
//     is refused with no second syscall and no TOCTOU window. This is what
//     keeps the index from ever naming a path outside the root.
//   - Entries are sorted and the queue is FIFO, so a completed scan is
//     reproducible and the picker's ordering is stable between runs.
//   - Project-specific exclusions from the session root .gitignore are parsed
//     once at scan start by internal/ignorefile, the same matcher the
//     permission layer refuses reads with. Directories matching ignore patterns
//     are pruned early from the traversal queue without visiting child entries,
//     and matching candidate files are skipped without extra syscalls.
//
// The limits are safety limits, not preferences. Scan stops at the first one
// that trips and says which one in Stop, so a caller can tell a complete index
// from a truncated one instead of guessing.
//
// DefaultTimeout measurement (android/arm64, Go 1.27.1, warm cache):
// 10,000 candidates in 35ms on a 600x20 synthetic tree (12,000 candidates),
// 57x margin below the 2s cap. With session root .gitignore pattern matching,
// twelve consecutive interleaved runs measured 26-43ms, a 46-75x margin; the
// narrower band once recorded here was therefore not a floor. The invariant is
// enforced rather than assumed: the scan with a .gitignore must stay within
// 2.5x of the identical tree without one, and must keep the >= 20x margin
// whenever the baseline clears 50x, the point past which the number is about
// the matcher and not about the host, in the plain build; race_enabled_test.go
// records why the race detector's run takes the functional assertions only.
// Guarded by the untagged
// TestDefaultTimeoutDoesNotBindBeforeTheCandidateLimit and
// TestGitignoreMaintainsPerformanceMarginOnWideTree in scan_test.go.
package pathindex

import (
	"io/fs"
	"os"
	"path"
	"sort"
	"time"

	"nabd/internal/ignorefile"
	"nabd/internal/tools"
)

// The measured limits. DefaultMaxCandidates must stay below DefaultMaxEntries:
// every candidate is also an entry, so equal values make the candidate limit
// unreachable. TestDefaultCandidateLimitIsReachable holds that invariant.
const (
	DefaultMaxEntries    = 50000
	DefaultMaxCandidates = 10000
	DefaultTimeout       = 2 * time.Second
)

// StopReason says why a scan ended. Anything other than StopComplete means the
// index is a prefix of the tree, not the tree.
type StopReason string

const (
	StopComplete   StopReason = "complete"
	StopEntries    StopReason = "entry-limit"
	StopCandidates StopReason = "candidate-limit"
	StopTimeout    StopReason = "timeout"
)

// DefaultExcluded is the directory-name filter, kept in step with skipDir in
// internal/tools/registry.go. Excluding .ag is a containment property, not a
// performance one: the content-addressed shadow history must never be offered
// back as a project path.
func DefaultExcluded(name string) bool {
	switch name {
	case ".git", ".ag", "node_modules", "vendor", ".venv", "__pycache__",
		"target", "dist", "build", ".next", ".cache", ".idea":
		return true
	}
	return false
}

// Config tunes a scan. The zero value is the measured configuration.
type Config struct {
	MaxEntries    int
	MaxCandidates int
	Timeout       time.Duration
	// Excluded reports whether a directory name is never entered. Nil means
	// DefaultExcluded.
	Excluded func(name string) bool

	// now is injected by the limit tests so they need no sleep.
	now func() time.Time
}

func (c Config) withDefaults() Config {
	if c.MaxEntries <= 0 {
		c.MaxEntries = DefaultMaxEntries
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = DefaultMaxCandidates
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Excluded == nil {
		c.Excluded = DefaultExcluded
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// Index is the result of a scan: the paths, and enough counters for a caller to
// report what the walk cost and whether it saw everything.
type Index struct {
	// Paths are root-relative and slash-separated, in the order the walk
	// produced them: breadth-first, each directory's entries sorted by name.
	Paths []string

	Entries      int
	DirsVisited  int
	Candidates   int
	Rejected     int
	ResolveCalls int

	Elapsed time.Duration
	Stop    StopReason
}

// Complete reports whether the index covers the whole tree. A caller that
// renders a picker should say so when it does not.
func (i Index) Complete() bool { return i.Stop == StopComplete }

// gitignorePattern, gitignoreMatcher, parseGitignore and match used to live
// here. They moved to internal/ignorefile when the permission layer had to
// refuse the same paths: one pattern implementation, two consumers. Scan's
// behaviour is unchanged by the move — TestScanGitignore* are the evidence.

// Scan walks root under cfg and returns the regular files inside it.
func Scan(root *tools.Root, cfg Config) Index {
	cfg = cfg.withDefaults()
	now := cfg.now
	start := now()

	var idx Index

	gi := ignorefile.LoadDir(root.Dir())

	type item struct {
		rel   string
		depth int
	}
	queue := []item{{rel: ".", depth: 0}}

	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]

		// One resolution per directory, not per candidate.
		abs, err := root.Resolve(dir.rel)
		idx.ResolveCalls++
		if err != nil {
			idx.Rejected++
			continue
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			idx.Rejected++
			continue
		}
		idx.DirsVisited++
		sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })

		for _, e := range entries {
			if idx.Entries >= cfg.MaxEntries {
				idx.Stop = StopEntries
				idx.Elapsed = now().Sub(start)
				return idx
			}
			if now().Sub(start) > cfg.Timeout {
				idx.Stop = StopTimeout
				idx.Elapsed = now().Sub(start)
				return idx
			}
			idx.Entries++

			name := e.Name()
			rel := name
			if dir.rel != "." {
				rel = path.Join(dir.rel, name)
			}

			// A symlink is refused as an entry: no second syscall, and no path
			// outside the root can enter the index through one.
			if e.Type()&fs.ModeSymlink != 0 {
				idx.Rejected++
				continue
			}
			if e.IsDir() {
				if _, ignored := gi.Match(rel, name, true); !cfg.Excluded(name) && !ignored {
					queue = append(queue, item{rel: rel, depth: dir.depth + 1})
				}
				continue
			}
			if !e.Type().IsRegular() {
				idx.Rejected++
				continue
			}
			if _, ignored := gi.Match(rel, name, false); ignored {
				continue
			}
			if idx.Candidates >= cfg.MaxCandidates {
				idx.Stop = StopCandidates
				idx.Elapsed = now().Sub(start)
				return idx
			}
			idx.Candidates++
			idx.Paths = append(idx.Paths, rel)
		}
	}

	idx.Stop = StopComplete
	idx.Elapsed = now().Sub(start)
	return idx
}
