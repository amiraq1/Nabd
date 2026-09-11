package main

import (
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/store"
)

// TestSessionPathConcurrentAllocationsAreDistinct proves that concurrent
// new-session allocations with the same frozen timestamp produce N
// distinct files, each holding exactly one logical session. On the
// unpatched naming (timestamp only), this test fails because two
// allocations resolve to the same path and their event trees interleave
// in one journal — duplicate Seq values, multiple roots, crossed
// Parent references.
func TestSessionPathConcurrentAllocationsAreDistinct(t *testing.T) {
	const n = 4
	tmp := t.TempDir()

	frozen := mustParseTime("20260911-120000.000")

	paths := make([]string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			p, err := sessionPathAt(tmp, frozen)
			if err != nil {
				errs <- err
				return
			}
			paths[idx] = p
			j, err := store.NewJSONL(p)
			if err != nil {
				errs <- err
				return
			}
			base := frozen
			evs := []agent.Event{
				{Seq: 1, Time: base, Type: agent.RunStart, Text: "session-" + string(rune('A'+idx)), ProjectRoot: "/repo"},
				{Seq: 2, Parent: 1, Time: base.Add(1e6), Type: agent.UserMsg, Text: "hello from session " + string(rune('A'+idx))},
				{Seq: 3, Parent: 2, Time: base.Add(2e6), Type: agent.TurnEnd},
			}
			for _, e := range evs {
				if err := j.Append(e); err != nil {
					errs <- err
					return
				}
			}
			if err := j.Close(); err != nil {
				errs <- err
				return
			}
			errs <- nil
		}(i)
	}
	var firstErr error
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		t.Fatalf("allocation/append failed: %v", firstErr)
	}

	// Distinct returned paths.
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Errorf("duplicate returned path %q (two logical sessions share one journal)", p)
		}
		seen[p] = true
	}

	// Distinct physical files, each containing exactly one session.
	for _, p := range paths {
		evs, err := store.Read(p)
		if err != nil {
			t.Errorf("read %q: %v", p, err)
			continue
		}
		roots := 0
		seqs := map[int]bool{}
		for _, e := range evs {
			if e.Parent == 0 {
				roots++
			}
			if seqs[e.Seq] {
				t.Errorf("duplicate Seq %d in %q — independent sessions interleaved in one journal", e.Seq, p)
			}
			seqs[e.Seq] = true
		}
		if roots != 1 {
			t.Errorf("%q has %d roots, want 1 (multiple roots from interleaved sessions)", p, roots)
		}
		if len(evs) != 3 {
			t.Errorf("%q: %d events, want 3 (isolated session expected)", p, len(evs))
		}
	}

	// Cross-file isolation: each file's RunStart text identifies its own
	// session. Reusing Seq=1,2,3 in independent files is expected.
	for _, p := range paths {
		evs, err := store.Read(p)
		if err != nil {
			continue
		}
		var txt string
		for _, e := range evs {
			if e.Type == agent.RunStart {
				txt = e.Text
			}
		}
		if !strings.HasPrefix(txt, "session-") {
			t.Errorf("%q RunStart text %q does not identify its session", p, txt)
		}
	}
}

func mustParseTime(s string) time.Time {
	t, err := time.Parse("20060102-150405.000", s)
	if err != nil {
		panic(err)
	}
	return t
}
