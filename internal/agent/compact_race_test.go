package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nabd/internal/provider"
)

// readyBlockingProvider is a fake summarisation provider that signals readyCh
// the moment its goroutine is about to block on unblock. This gives the test
// a deterministic interleave point without relying on scheduling or sleeps.
type readyBlockingProvider struct {
	readyCh chan<- struct{}
	unblock <-chan struct{}
}

func (b *readyBlockingProvider) Name() string { return "ready-blocking" }

func (b *readyBlockingProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	go func() {
		defer close(ch)
		b.readyCh <- struct{}{} // signal: summarisation is now blocked
		<-b.unblock             // deterministic release point for the test
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "summary text"}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

// countingProvider counts Stream calls and answers immediately. It lets a test
// prove that a rejected Compact never paid for a summarisation request.
type countingProvider struct{ calls int }

func (c *countingProvider) Name() string { return "counting" }

func (c *countingProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	c.calls++
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	close(ch)
	return ch, nil
}

// seqPresentInLive reports whether an event with the given Seq is reachable in
// the current Live() branch (not merely present somewhere in the journal).
func seqPresentInLive(l *Loop, seq int) bool {
	for _, e := range Live(l.Hist()) {
		if e.Seq == seq {
			return true
		}
	}
	return false
}

// TestCompactBoundaryValidationRace verifies that Compact rejects a compaction
// whose selected boundary has been removed from the live branch by a concurrent
// rewind.
//
// The load-bearing Finding 2 defect is NOT the raw tool-pairing invariant.
// Under production emission ordering (UserMsg emitted only at the top of
// Run, Seq monotonically increasing), no compaction boundary — which is always
// a UserMsg — can land between a ToolStart and its ToolEnd, so an orphaned
// ToolEnd cannot be produced by the running journal. Raw pairing is defense in
// depth only.
//
// The reachable defect is the /compact × /rewind overlap: /compact runs in a
// detached goroutine outside the m.running interlock, so a concurrent /rewind can
// remove firstKept from the live branch while summarisation is blocked. Baseline
// Compact (HEAD) searches the stale snapshot, never detects the loss, and
// appends a Compact event whose FirstKept names an event no longer on the live
// branch. The corrected Compact searches fresh history under l.mu and rejects
// with ErrCompactBoundaryStale, appending nothing.
//
// Subtests:
//   - rejects_boundary_dropped_by_concurrent_rewind: the load-bearing regression.
//     A concurrent Rewind removes firstKept from the live branch while
//     summarisation is blocked; Compact must reject with ErrCompactBoundaryStale
//     and append nothing. (Production-reachable via the real Rewind path.)
//   - rejects_orphaned_tool_end (SYNTHETIC / defense in depth): constructed via
//     direct l.emit to create an ordering the running journal cannot produce;
//     validates the secondary raw-pairing guard only.
//   - accepts_safe_concurrent_append: a concurrent turn appends complete,
//     well-paired events; Compact must succeed and prove safe boundaries are not
//     over-rejected.
//   - accepts_malformed_tool_end_without_blocking_compaction: malformed ToolEnd
//     events (Call == nil or Call.ID == "") from legacy journals do not fail the
//     pairing invariant, preventing permanent bricking of /compact on --continue.
//   - pre_validation_rejects_without_calling_provider: an already-unsafe snapshot
//     boundary is rejected before summarise runs, so no provider request is paid.
func TestCompactBoundaryValidationRace(t *testing.T) {
	// SYNTHETIC / defense in depth. The orphaned-ToolEnd ordering here is
	// manufactured via direct l.emit; it cannot be produced by the running
	// journal (a compaction boundary is always a UserMsg emitted only at the top
	// of Run, so no boundary can land between a ToolStart and its ToolEnd). This
	// subtest validates the secondary raw-pairing guard only — it is NOT the
	// reachable Finding 2 defect.
	t.Run("rejects_orphaned_tool_end", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		unblock := make(chan struct{})
		prov := &readyBlockingProvider{readyCh: ready, unblock: unblock}

		l := &Loop{
			Provider: prov,
			Budget:   NewBudget(),
		}

		// Turn 1 has large text (~400 tokens), plus a ToolStart.
		// Target is 50 tokens.
		orphanID := "orphan-call-1"
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})
		l.emit(Event{Type: TurnStart})
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: orphanID, Name: "read_file"}})

		// Turn 2 has short text (~5 tokens).
		// Because Turn 1 + Turn 2 exceeds target (400 > 50), chooseBoundaryWith
		// chooses Turn 2 as firstKept. The ToolStart in Turn 1 is therefore in the
		// dropped segment (live[:best]).
		l.emit(Event{Type: UserMsg, Text: "short user turn two"})
		turn2Seq := l.Hist()[len(l.Hist())-1].Seq

		target := 50

		// R4: Assert explicitly that chooseBoundaryWith chooses Turn 2's Seq.
		// Production calls it on Live(l.hist); matching that here ensures the
		// assertion guards the boundary the real code would pick.
		expectedBoundary, _, ok := chooseBoundaryWith(Live(l.Hist()), target, l.estimateMessages)
		if !ok || expectedBoundary != turn2Seq {
			t.Fatalf("test setup failure: chooseBoundaryWith chose seq %d (ok=%v), want Turn 2 seq %d", expectedBoundary, ok, turn2Seq)
		}

		histBeforeCompact := l.Hist()
		compactHistLen := len(histBeforeCompact)

		compactErr := make(chan error, 1)
		go func() {
			compactErr <- l.Compact(context.Background(), target)
		}()

		// Wait until summarise is running and blocked in the fake provider.
		<-ready

		// While summarise is blocked, the concurrent execution completes the
		// tool call from Turn 1. This ToolEnd has Seq > firstKept, so it enters
		// the retained segment, but its originating ToolStart is in the dropped
		// segment.
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: orphanID, OK: true, Output: "tool finished"}})
		l.emit(Event{Type: TurnEnd})

		// Release summarise.
		close(unblock)
		err := <-compactErr

		// Under baseline Compact (HEAD), Compact erroneously succeeds (err == nil)
		// and appends a Compact event whose retained segment has ToolEnd without
		// ToolStart. Under the corrected Compact, Compact returns ErrCompactBoundaryStale
		// and appends no Compact event.
		if err == nil {
			// On baseline Compact, check the retained live history.
			liveAfter := Live(l.Hist())
			if !rawPairingInvariantHolds(liveAfter) {
				t.Fatalf("raw pairing invariant violated: ToolEnd %q retained without preceding ToolStart; baseline Compact permitted unsafe compaction", orphanID)
			}
			t.Fatalf("expected Compact to fail with ErrCompactBoundaryStale, got nil")
		}

		if !errors.Is(err, ErrCompactBoundaryStale) {
			t.Fatalf("expected ErrCompactBoundaryStale, got %v", err)
		}

		// Verify no Compact event was appended on rejection.
		for _, e := range l.Hist()[compactHistLen:] {
			if e.Type == Compact {
				t.Fatalf("Compact event was appended despite ErrCompactBoundaryStale rejection")
			}
		}

		// Verify the full live history remains valid.
		if !rawPairingInvariantHolds(Live(l.Hist())) {
			t.Fatalf("raw pairing invariant violated on live history after rejection")
		}
	})

	t.Run("accepts_safe_concurrent_append", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		unblock := make(chan struct{})
		prov := &readyBlockingProvider{readyCh: ready, unblock: unblock}

		l := &Loop{
			Provider: prov,
			Budget:   NewBudget(),
		}

		// Seed Turn 1 (large text to force cutoff) and Turn 2 (short text).
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})
		l.emit(Event{Type: UserMsg, Text: "short user turn two"})

		target := 50
		compactErr := make(chan error, 1)
		go func() {
			compactErr <- l.Compact(context.Background(), target)
		}()

		<-ready

		// Append a harmless, complete tool turn while summarisation is blocked.
		safeID := "safe-call-1"
		l.emit(Event{Type: UserMsg, Text: "concurrent user turn three"})
		l.emit(Event{Type: TurnStart})
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: safeID, Name: "read_file"}})
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: safeID, OK: true, Output: "read output"}})
		l.emit(Event{Type: TurnEnd})

		close(unblock)
		err := <-compactErr

		if err != nil {
			t.Fatalf("expected Compact to succeed on safe concurrent append, got %v", err)
		}

		// Verify a Compact event was appended.
		hist := l.Hist()
		var compactEv *Event
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].Type == Compact {
				compactEv = &hist[i]
				break
			}
		}
		if compactEv == nil {
			t.Fatalf("expected Compact event to be appended")
		}

		// Verify the live history satisfies the raw pairing invariant.
		liveAfter := Live(l.Hist())
		if !rawPairingInvariantHolds(liveAfter) {
			t.Fatalf("raw pairing invariant violated on live history after successful compact")
		}

		// Verify fresh MessagesBefore reflects the fresh history under lock.
		if compactEv.Compact == nil || compactEv.Compact.MessagesBefore < 3 {
			t.Fatalf("expected MessagesBefore to reflect fresh history (>=3 messages), got %+v", compactEv.Compact)
		}
	})

	t.Run("accepts_malformed_tool_end_without_blocking_compaction", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		unblock := make(chan struct{})
		prov := &readyBlockingProvider{readyCh: ready, unblock: unblock}
		close(unblock)

		l := &Loop{
			Provider: prov,
			Budget:   NewBudget(),
		}

		// Turn 1: large text to force compaction cutoff.
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})

		// Turn 2: includes legacy/corrupted ToolEnd events (one with nil Call, one with empty ID).
		l.emit(Event{Type: UserMsg, Text: "turn two with legacy events"})
		l.emit(Event{Type: TurnStart})
		l.emit(Event{Type: ToolEnd, Call: nil})
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: "", Name: "read_file", Output: "some output"}})
		l.emit(Event{Type: TurnEnd})

		target := 50
		if err := l.Compact(context.Background(), target); err != nil {
			t.Fatalf("expected Compact to succeed despite malformed ToolEnd events, got %v", err)
		}

		// Confirm a Compact event was appended and live history invariant holds.
		hist := l.Hist()
		if len(hist) == 0 || hist[len(hist)-1].Type != Compact {
			t.Fatalf("expected Compact event at tail of history")
		}
		if !rawPairingInvariantHolds(Live(hist)) {
			t.Fatalf("rawPairingInvariantHolds should not fail on malformed ToolEnd")
		}
	})

	t.Run("pre_validation_rejects_without_calling_provider", func(t *testing.T) {
		prov := &countingProvider{}
		l := &Loop{Provider: prov, Budget: NewBudget()}

		// Turn 1 is large; Turn 2 is short and carries a ToolEnd whose ToolStart
		// sits in the dropped segment, so the snapshot's retained segment is
		// already unsafe when Compact starts.
		orphanID := "pre-orphan-1"
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("bulk-turn-one-text-", 100)})
		l.emit(Event{Type: TurnStart})
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: orphanID, Name: "read_file"}})
		l.emit(Event{Type: UserMsg, Text: "short user turn two"})
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: orphanID, OK: true, Output: "tool finished"}})
		l.emit(Event{Type: TurnEnd})

		target := 50
		histBefore := len(l.Hist())
		err := l.Compact(context.Background(), target)

		if !errors.Is(err, ErrCompactBoundaryStale) {
			t.Fatalf("expected ErrCompactBoundaryStale, got %v", err)
		}
		if prov.calls != 0 {
			t.Fatalf("an already-unsafe snapshot must be rejected before summarise; provider was called %d time(s)", prov.calls)
		}
		for _, e := range l.Hist()[histBefore:] {
			if e.Type == Compact {
				t.Fatalf("Compact event appended despite rejection")
			}
		}
	})

	// rejects_boundary_dropped_by_concurrent_rewind is the load-bearing
	// regression test for the reachable Finding 2 defect: because /compact runs
	// in a detached goroutine outside the m.running interlock, a concurrent
	// /rewind can remove the selected firstKept from the live branch while
	// summarisation is blocked. Baseline Compact (HEAD) searches the stale
	// snapshot, never detects the loss, and appends a Compact event whose
	// FirstKept names an event no longer on the live branch. The corrected
	// Compact searches fresh history under l.mu and rejects with
	// ErrCompactBoundaryStale, appending nothing.
	//
	// The seeded event order below is production-reachable: it is a prefix of a
	// real session (large Turn 1 mid-tool-execution, then a short Turn 2) and
	// does not itself manufacture the defect — the defect is introduced by the
	// concurrent Rewind, which is the actual production Rewind path.
	t.Run("rejects_boundary_dropped_by_concurrent_rewind", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		unblock := make(chan struct{})
		prov := &readyBlockingProvider{readyCh: ready, unblock: unblock}
		l := &Loop{Provider: prov, Budget: NewBudget()}

		// Turn 1 is large (forces cutoff) and is the SURVIVOR: it survives the
		// intentional Rewind, sits below firstKept, and must remain present in
		// the live projection. Turn 2 is short and is firstKept.
		turn1Content := strings.Repeat("bulk-turn-one-text-", 100)
		l.emit(Event{Type: UserMsg, Text: turn1Content})
		turn1Seq := l.Hist()[len(l.Hist())-1].Seq
		l.emit(Event{Type: TurnStart})
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: "open-call", Name: "read_file"}})
		l.emit(Event{Type: UserMsg, Text: "short user turn two"})
		turn2Seq := l.Hist()[len(l.Hist())-1].Seq

		target := 50

		// Confirm setup: Turn 2 is the boundary Compact will select.
		expectedFirstKept, _, ok := chooseBoundaryWith(Live(l.Hist()), target, l.estimateMessages)
		if !ok || expectedFirstKept != turn2Seq {
			t.Fatalf("setup failure: chooseBoundaryWith chose seq %d (ok=%v), want Turn 2 seq %d", expectedFirstKept, ok, turn2Seq)
		}

		compactErr := make(chan error, 1)
		go func() { compactErr <- l.Compact(context.Background(), target) }()

		// Block until summarisation is running in the fake provider.
		<-ready

		// Concurrent Rewind removes Turn 2 — which IS firstKept — from the live
		// branch, while Compact is blocked in summarise.
		if _, err := l.Rewind(1); err != nil {
			t.Fatalf("rewind failed: %v", err)
		}

		// Post-rewind integrity (before releasing summarisation): firstKept must
		// be gone from the live branch, and the survivor must still be present.
		// Any later disappearance of the survivor is therefore caused by stale
		// compact handling, not by Rewind itself.
		if seqPresentInLive(l, turn2Seq) {
			t.Fatalf("setup failure: firstKept seq %d still present in live branch after rewind", turn2Seq)
		}
		if !seqPresentInLive(l, turn1Seq) {
			t.Fatalf("setup failure: survivor Turn 1 seq %d missing from live branch immediately after rewind", turn1Seq)
		}
		postRewindLive := Live(l.Hist())

		// Release summarisation.
		close(unblock)
		err := <-compactErr
		liveAfter := Live(l.Hist())

		// The survivor must remain present in the live projection. This is the
		// load-bearing assertion: on baseline, the stale Compact event re-heads
		// the projection and drops every event whose Seq is below the stale
		// threshold, collapsing the live context to [Compact, Rewind].
		if !seqPresentInLive(l, turn1Seq) {
			var types []string
			for _, e := range liveAfter {
				types = append(types, string(e.Type))
			}
			t.Fatalf(
				"live-history collapse: survivor Turn 1 seq %d (content %q) disappeared from live projection after stale compact;"+
					" final live types=%v final live seqs=%v postRewindLiveLen=%d",
				turn1Seq, turn1Content[:20], types, seqsOf(liveAfter), len(postRewindLive),
			)
		}

		// Corrected Compact must reject with the stale-boundary sentinel.
		if !errors.Is(err, ErrCompactBoundaryStale) {
			t.Fatalf("expected ErrCompactBoundaryStale after concurrent rewind removed firstKept, got %v", err)
		}

		// No Compact event must be appended on rejection.
		for _, e := range l.Hist() {
			if e.Type == Compact {
				t.Fatalf("Compact event was appended despite ErrCompactBoundaryStale rejection")
			}
		}

		// The journal/live history must remain interpretable.
		if !rawPairingInvariantHolds(liveAfter) {
			t.Fatalf("raw pairing invariant violated on live history after rejection")
		}
	})
}

// seqsOf returns the Seq values of events in order.
func seqsOf(evs []Event) []int {
	s := make([]int, 0, len(evs))
	for _, e := range evs {
		s = append(s, e.Seq)
	}
	return s
}
