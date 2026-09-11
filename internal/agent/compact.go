// Package agent: compact.go writes one entry and changes what the session
// means. It never rewrites the file: first_kept moves the boundary, Live()
// obeys it, and every dropped event stays on disk for the replay.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nabd/internal/provider"
)

const summaryPrompt = `You are summarising a coding session so it can continue after context compaction.
Write a brief summary mentioning: what the user asked, what was actually done, files read or modified by name, decisions taken and why, and what remains pending.
Do not apologise, do not greet, do not invent what did not happen. Leave out points you do not know.`

// Compact cuts history at the newest user turn whose tail fits in target,
// summarises everything before it, and appends one Compact entry.
//
// The selected boundary is re-validated against the *fresh* live history under
// l.mu after provider summarisation, and the Compact event is appended within
// the same critical section, so no mutation can interleave between the final
// check and the append. If the chosen firstKept is absent from the current live
// branch — which a concurrent /rewind can cause because /compact runs in a
// detached goroutine outside the m.running interlock (cmd/ag/main.go) — Compact
// returns ErrCompactBoundaryStale and appends nothing.
//
// A secondary raw tool-event-pairing check runs as defense in depth. It cannot
// fire under current production ordering (a boundary is always a UserMsg
// emitted only at the top of Run; Seq increases in emission order), so it is
// only reachable through direct event injection or a --continue of a journal
// that was already structurally corrupt. The harm it guards against is a
// fabricated tool_use attributed to the assistant — Messages() synthesises a
// wire-valid tool_use for an unmatched ToolEnd, so the request is never
// provider-rejected — not an orphaned tool_result. See messages.go's ToolEnd case.
func (l *Loop) Compact(ctx context.Context, target int) error {
	if !l.historyMu.TryLock() {
		return ErrHistoryMutationInProgress
	}
	defer l.historyMu.Unlock()

	// Phase 1: take a snapshot of the live branch to pick a boundary.
	// l.mu is held only briefly so that the provider call does not block it.
	l.mu.Lock()
	live := Live(l.hist)
	l.mu.Unlock()

	firstKept, dropped, ok := chooseBoundaryWith(live, target, l.estimateMessages)
	if !ok {
		return fmt.Errorf("no valid boundary (%d live events)", len(live))
	}

	// Phase 1b: cheap pre-validation (defense in depth).
	//
	// If the snapshot's proposed retained segment already violates the raw
	// pairing invariant, reject before paying for a provider summarisation
	// request. The load-bearing check is Phase 3, which re-validates the fresh
	// history under l.mu; this guard only avoids a wasted request on an
	// already-unsafe snapshot (e.g. a journal resumed with --continue that is
	// missing a ToolStart).
	//
	// An intervening append cannot turn a snapshot that fails here into a safe
	// fresh history: appends land after the retained segment, so they can never
	// supply a ToolStart that precedes an already-retained ToolEnd. Only a
	// concurrent rewind could drop the offending ToolEnd, and rejecting a
	// snapshot that was unsafe at the moment it was read is the fail-closed
	// choice for that case, not an over-rejection.
	if !rawPairingInvariantHolds(live[len(dropped):]) {
		return ErrCompactBoundaryStale
	}
	promptEstimate := EstimateMessages(Messages(dropped))
	if l.Budget != nil {
		promptEstimate = l.Budget.Estimate(Messages(dropped))
	}
	if err := l.SpendBudget.Charge(promptEstimate, 0, maxOutputTokens()); err != nil {
		return err
	}

	// Phase 2: summarise without holding l.mu (provider round-trip may be slow).
	sum := l.summarise(ctx, dropped)

	// Phase 3: re-acquire l.mu and atomically validate the boundary against the
	// current history, then append if and only if the projection is safe.
	l.mu.Lock()
	freshLive := Live(l.hist)

	// Find firstKept in the fresh history.
	boundary := -1
	for i, e := range freshLive {
		if e.Seq == firstKept {
			boundary = i
			break
		}
	}
	if boundary < 0 {
		// firstKept was dropped by a concurrent rewind or other operation.
		l.mu.Unlock()
		return ErrCompactBoundaryStale
	}

	// Verify the proposed retained raw segment directly: for every ToolEnd
	// event in freshLive[boundary:], a matching ToolStart event with the same
	// tool-call ID must precede it in that retained segment. We validate raw
	// events directly rather than calling Messages(), because Messages()
	// synthesizes missing ToolCalls for archive compatibility and would
	// mask the corruption.
	retained := freshLive[boundary:]
	if !rawPairingInvariantHolds(retained) {
		l.mu.Unlock()
		return ErrCompactBoundaryStale
	}

	// The boundary is safe. Compute statistics from the fresh state and append
	// the compact event atomically under the same lock acquisition.
	freshBefore := Messages(freshLive) // full current projection before compaction
	keptMsgs := Messages(retained)
	after := Squeeze(keptMsgs, l.keepFullRounds())
	compactEvent := Event{
		Type:      Compact,
		FirstKept: firstKept,
		Text:      sum,
		Compact: &CompactionStats{
			MessagesBefore: len(freshBefore),
			MessagesAfter:  len(after),
			TokensBefore:   l.estimateMessages(freshBefore),
			TokensAfter:    l.estimateMessages(after),
			// BoundaryIndex indexes the FRESH live slice (freshLive) computed
			// under l.mu, not the Phase-1 snapshot: a concurrent append can
			// shift it relative to the snapshot. It is a diagnostic, not a
			// contract.
			BoundaryIndex: boundary,
			Stubs:         countReadStubs(after),
		},
	}
	err := l.emitLocked(l.parent, compactEvent)
	l.mu.Unlock()
	return err
}

func countReadStubs(ms []provider.Message) int {
	count := 0
	for _, m := range ms {
		for _, r := range m.ToolResults {
			if strings.Contains(r.Output, "content squeezed") {
				count++
			}
		}
	}
	return count
}

// chooseBoundary walks user turns from newest to oldest and takes the oldest
// tail that still fits. The boundary is always a user message: cutting inside
// a round leaves a retained ToolEnd whose ToolStart was dropped, and Messages()
// then synthesises a fabricated tool_use for it instead of surfacing a call the
// model really made. (The wire payload stays valid — the harm is fabrication,
// not rejection; see Compact's own doc comment.)
func chooseBoundary(live []Event, target int) (int, []Event, bool) {
	return chooseBoundaryWith(live, target, EstimateMessages)
}

func chooseBoundaryWith(live []Event, target int, estimate MessageEstimator) (int, []Event, bool) {
	var idx []int
	for i, e := range live {
		if e.Type == UserMsg {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return 0, nil, false
	}
	best := -1
	for k := len(idx) - 1; k >= 0; k-- {
		i := idx[k]
		if estimate(Messages(live[i:])) > target && best >= 0 {
			break
		}
		best = i
	}
	if best <= 0 {
		return 0, nil, false
	}
	return live[best].Seq, live[:best], true
}

func (l *Loop) summarise(ctx context.Context, dropped []Event) string {
	mech := mechanicalSummary(dropped)
	if l.Provider == nil {
		return mech
	}
	ms := append(Messages(dropped), provider.Message{
		Role: provider.User,
		Text: "Summarise what came before per the instructions.",
	})
	ch, err := l.Provider.Stream(ctx, provider.Request{
		Messages: ms,
		System:   summaryPrompt,
	})
	if err != nil {
		return mech
	}
	var b strings.Builder
	for c := range ch {
		if c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return mech // a failed summariser must never cost the thread
	}
	return s + "\n\n" + mech
}

// argSummaryPath extracts the 'path' key from raw JSON args.
func argSummaryPath(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	if v, ok := m["path"]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

// mechanicalSummary is the floor: no network, no model, always available.
// The verbatim user turns matter most — intent is the one thing a paraphrase
// is most likely to bend.
func mechanicalSummary(evs []Event) string {
	var asks []string
	files := map[string]bool{}
	errs := 0
	for _, e := range evs {
		switch e.Type {
		case UserMsg:
			t := e.Text
			if len(t) > 160 {
				t = t[:160] + "…"
			}
			asks = append(asks, "- "+t)
		case ToolEnd:
			if e.Call == nil {
				continue
			}
			if p := argSummaryPath(e.Call.Args); p != "" {
				files[p] = true
			}
			if !e.Call.OK {
				errs++
			}
		}
	}
	var b strings.Builder
	b.WriteString("«brief log»\nUser requests:\n")
	b.WriteString(strings.Join(asks, "\n"))
	if len(files) > 0 {
		var fs []string
		for f := range files {
			fs = append(fs, f)
		}
		fmt.Fprintf(&b, "\nFiles touched by tools: %s", strings.Join(fs, ", "))
	}
	if errs > 0 {
		fmt.Fprintf(&b, "\nTool errors: %d", errs)
	}
	return b.String()
}
