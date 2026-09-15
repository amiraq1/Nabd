//go:build measure

// Phase 0 of the @-picker, continued: the 2s cap is the one approved limit that
// was never measured. The entry and candidate limits each have a row that trips
// them, but every tree measured so far is either too small to reach a ceiling
// (the repository root completes at ~495 entries) or stops on the entry or
// candidate limit first, so limitTimeout has only ever been asserted, never read
// off a machine.
//
// This file adds the missing row. It measures a tree wide enough to reach the
// candidate limit and asks one question: does the 2s cap trip before the
// candidate limit does? If it does, the cap is not a safety valve, it is the
// binding limit, and the phase 1 spec may not describe it as a valve. If it does
// not, the row prints the real time to the candidate limit and the margin
// against the cap, which is the number the spec should carry.
//
// Run it with:
//
//	NABD_MEASURE_CUTOFF=60 go test -tags measure ./internal/pathindex/ -run TestMeasureTimeout -v -count=1
//
// NABD_MEASURE_TIMEOUT_DIRS and NABD_MEASURE_TIMEOUT_FILES shape the synthetic
// tree (default 5000 x 20: 100,000 candidates in ~105,000 entries, which is ten
// times the candidate limit so the walk is never within sight of completing).
package pathindex

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

func timeoutTreeShape() (dirs, files int) {
	dirs, files = 5000, 20
	if s := os.Getenv("NABD_MEASURE_TIMEOUT_DIRS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			dirs = n
		}
	}
	if s := os.Getenv("NABD_MEASURE_TIMEOUT_FILES"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			files = n
		}
	}
	return dirs, files
}

// timeToCandidates reads the recorded milestone for a candidate count, so the
// number comes from the counter rather than from the total wall time.
func timeToCandidates(s scanResult, want int) (time.Duration, bool) {
	for _, m := range s.milestones {
		if m.candidates == want {
			return m.elapsed, true
		}
	}
	return 0, false
}

// TestMeasureTimeoutValveIsNotBinding measures the 2s cap on a tree that reaches
// the candidate limit. It fails when the cap trips first, because that is the
// case the spec may not silently document as a safety valve.
func TestMeasureTimeoutValveIsNotBinding(t *testing.T) {
	dirs, files := timeoutTreeShape()
	root := buildTree(t, dirs, files)
	fmt.Printf("\nenvironment: %s\nmethod: %d rounds at the production limits on a synthetic %dx%d tree (~%d entries, %d candidates, %.0fx the candidate limit)\nquestion: does the %v cap trip before the %d candidate limit?\nroot: %s\n",
		envLine(), measureRounds(), dirs, files, dirs*(files+1)+dirs, dirs*files,
		float64(dirs*files)/float64(limitCandidates), limitTimeout, limitCandidates, root.Dir())

	for round := 0; round < measureRounds(); round++ {
		s := scan(root, scanConfig{maxEntries: limitEntries, maxCandidates: limitCandidates, timeout: limitTimeout, excluded: shortListV0})
		reportOne(fmt.Sprintf("cap on, round %d", round+1), s, scanConfig{})
		if s.stop == stopTimeout {
			t.Fatalf("round %d stopped on the %v cap after %v (entries=%d candidates=%d): the cap is the binding limit on this tree, so it cannot be documented as a safety valve",
				round+1, limitTimeout, s.elapsed.Round(time.Millisecond), s.entries, s.candidates)
		}
	}

	// Cap lifted: what reaching the candidate limit actually costs, and the margin
	// the cap leaves above it.
	s := scan(root, scanConfig{maxEntries: limitEntries, maxCandidates: limitCandidates, timeout: measureCutoff(), excluded: shortListV0})
	reportOne("cap lifted", s, scanConfig{})
	if s.stop != stopCandidates {
		t.Fatalf("stop = %q, want %q: this tree does not reach the candidate limit, so it measures nothing about the cap", s.stop, stopCandidates)
	}
	elapsed, ok := timeToCandidates(s, limitCandidates)
	if !ok {
		t.Fatalf("no milestone recorded at %d candidates: measureMilestones must contain the candidate limit for this row to report a margin", limitCandidates)
	}
	fmt.Printf("   %-26s %d candidates in %v · cap=%v · margin=%.1fx · entries=%d · resolve=%d\n",
		"verdict", limitCandidates, elapsed.Round(time.Millisecond), limitTimeout,
		float64(limitTimeout)/float64(elapsed), s.entries, s.resolveCalls)
}
