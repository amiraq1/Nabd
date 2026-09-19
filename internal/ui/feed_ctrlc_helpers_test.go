package ui

import (
	"runtime"
	"testing"
	"time"
)

// Timeouts. These are failure ceilings, not pacing. Nothing in this file
// sleeps for a fixed duration to "let things settle" — every wait either ends
// early on a real signal or fails the test.
const (
	ctrlCSettleTimeout = 2 * time.Second
	// decisionGrace is the window a negative assertion needs: proving that
	// no approval was delivered requires giving a racing sender a fair chance
	// to deliver one. Keep it small; it is paid only by the 3 gate tests.
	decisionGrace = 50 * time.Millisecond
	pollBackoff   = 200 * time.Microsecond
)

// ─── runner access ───────────────────────────────────────────────────────────

// feedRunnerOf returns the recorder installed by feedWithBlockingRunner.
// It takes *testing.T (unlike the sketch in the contract file) so a wrong
// build path fails loudly instead of nil-panicking deep inside a helper.
func feedRunnerOf(t *testing.T, f *Feed) *runnerRecorder {
	t.Helper()
	r, ok := f.runner.(*runnerRecorder)
	if !ok {
		t.Fatalf("feed.runner is %T, want *runnerRecorder — build the feed with feedWithBlockingRunner", f.runner)
	}
	return r
}

// ─── settleRun ───────────────────────────────────────────────────────────────

// settleRun drives the feed from "cancellation requested" to "fully idle",
// the same way the real tea loop would, and fails if any step of that
// transition is missing.
//
// It exists because the Ctrl-C ladder's quit barrier depends on running/busy
// being FALSE. A test that flips those fields by hand would prove nothing:
// the contract is that a canceled run actually clears them on its own.
//
// Three distinct failures are distinguished, because they have three
// different owners:
//
//	(a) runner goroutine never exits  -> the run ignores ctx.Done()
//	(b) no terminal message arrives   -> the runner exits without reporting
//	(c) flags stay set after Update   -> the feed's Update ignores the report
func settleRun(t *testing.T, f *Feed) {
	t.Helper()
	r := feedRunnerOf(t, f)

	// (a) the run must observe cancellation and unwind by itself.
	waitUntil(t, r.finished, ctrlCSettleTimeout,
		"runner goroutine did not exit after cancellation — the run is ignoring ctx.Done()")

	// (b)+(c) pump whatever the runner emitted into the model, in order.
	deadline := time.Now().Add(ctrlCSettleTimeout)
	pumped := 0
	for f.running || f.busy {
		msg, ok := r.takeMsg()
		if !ok {
			if time.Now().After(deadline) {
				t.Fatalf("feed stuck at running=%v busy=%v after %d message(s); "+
					"runner exited without emitting a terminal msg (runDoneMsg/runCanceledMsg)",
					f.running, f.busy, pumped)
			}
			runtime.Gosched()
			time.Sleep(pollBackoff)
			continue
		}
		pumped++
		next, _ := f.Update(msg)
		np, ok := next.(*Feed)
		if !ok || np != f {
			t.Fatalf("Feed.Update returned %T (identity changed); settleRun assumes a pointer receiver", next)
		}
		if pumped > 64 {
			t.Fatalf("pumped %d messages and the feed is still busy; suspected message loop", pumped)
		}
	}

	// Post-conditions the quit barrier silently relies on.
	if f.secretPrompt || f.search.active {
		t.Fatalf("after settle: secretPrompt=%v searchActive=%v — a lower barrier is still armed",
			f.secretPrompt, f.search.active)
	}
	if got := composerTextOf(f); got != "" {
		t.Fatalf("after settle: composer = %q, want empty — the quit barrier will not be reached", got)
	}
}

// ─── decisionWasApproved ─────────────────────────────────────────────────────

// decisionWasApproved reports whether Ctrl-C leaked an answer to the
// permission gate. It waits decisionGrace on purpose: a non-blocking read
// immediately after onCtrlC would pass even if an approval were in flight on
// another goroutine, which is precisely the bug this test exists to catch.
//
// Returns true only for an actual approval. A denial is also a contract
// violation (Ctrl-C must leave the decision pending, not resolve it), so the
// caller gets a fatal for that case here rather than a silent false.
func decisionWasApproved(t *testing.T, f *Feed) bool {
	t.Helper()
	r := feedRunnerOf(t, f)
	ch := r.decisions()
	if ch == nil {
		t.Fatal("runnerRecorder exposes no decision channel; the gate assertion cannot be made")
	}

	select {
	case d, open := <-ch:
		if !open {
			// A closed channel makes the gate read a zero value. Even if that
			// zero value happens to mean "deny" today, it is an implicit
			// contract and must be rejected explicitly.
			t.Fatal("Ctrl-C closed the decision channel; the gate must keep waiting, not read a zero value")
		}
		if decisionIsAllow(d) {
			return true
		}
		t.Fatalf("Ctrl-C delivered decision %+v; the contract is that no decision is delivered at all", d)
		return false
	case <-time.After(decisionGrace):
		return false // nothing delivered within the grace window: contract held
	}
}

// assertNoDecisionDelivered is the positive-phrased wrapper used by the
// contract file's loop; prefer it at call sites for readability.
func assertNoDecisionDelivered(t *testing.T, f *Feed) {
	t.Helper()
	if decisionWasApproved(t, f) {
		t.Fatal("Ctrl-C approved a pending permission decision")
	}
}

// ─── generic wait ────────────────────────────────────────────────────────────

// waitUntil polls cond until it holds or the timeout elapses. Used only where
// the event source is a goroutine exit with no channel to select on.
func waitUntil(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s (waited %s)", msg, timeout)
		}
		runtime.Gosched()
		time.Sleep(pollBackoff)
	}
}
