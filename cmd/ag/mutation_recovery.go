package main

import (
	"fmt"

	"nabd/internal/agent"
	"nabd/internal/tools"
)

func mutationRecordKey(rec *agent.EditRecord) string {
	if rec == nil {
		return ""
	}
	if rec.MutationID != "" {
		return rec.MutationID
	}
	return rec.Path + "\x00" + rec.HashBefore + "\x00" + rec.HashAfter + "\x00" + rec.BlobAfter
}

// unresolvedMutationIntents reports intents on the active branch that have no
// committed edit or abort event. It is deliberately diagnostic only: a crash
// may have happened before or after publication, so continuation must never
// replay the mutation automatically.
func unresolvedMutationIntents(evs []agent.Event) int {
	type state struct {
		intent bool
		closed bool
	}
	states := map[string]state{}
	for _, e := range agent.Live(evs) {
		if e.Edit == nil {
			continue
		}
		key := mutationRecordKey(e.Edit)
		if key == "" {
			continue
		}
		s := states[key]
		switch e.Type {
		case agent.EventEditIntent:
			s.intent = true
		case agent.EventEdit, agent.EventEditAbort:
			s.closed = true
		}
		states[key] = s
	}
	count := 0
	for _, s := range states {
		if s.intent && !s.closed {
			count++
		}
	}
	return count
}

func unresolvedMutationRecords(evs []agent.Event) []*agent.EditRecord {
	type state struct {
		record *agent.EditRecord
		intent bool
		closed bool
	}
	states := map[string]state{}
	for _, e := range agent.Live(evs) {
		if e.Edit == nil {
			continue
		}
		key := mutationRecordKey(e.Edit)
		if key == "" {
			continue
		}
		s := states[key]
		if s.record == nil {
			s.record = e.Edit
		}
		switch e.Type {
		case agent.EventEditIntent:
			s.intent = true
		case agent.EventEdit, agent.EventEditAbort:
			s.closed = true
		}
		states[key] = s
	}
	out := make([]*agent.EditRecord, 0)
	for _, s := range states {
		if s.intent && !s.closed {
			out = append(out, s.record)
		}
	}
	return out
}

func noteMutationRecovery(loop *agent.Loop, reg *tools.Registry, evs []agent.Event) {
	recs := unresolvedMutationRecords(evs)
	if len(recs) == 0 {
		return
	}
	counts := map[tools.MutationRecoveryState]int{}
	errorsSeen := 0
	for _, rec := range recs {
		state, err := reg.ReconcileMutation(rec)
		if err != nil {
			errorsSeen++
			continue
		}
		counts[state]++
	}
	loop.Note(fmt.Sprintf(
		"recovery: %d unresolved mutation intent(s) · not_published=%d published=%d missing=%d conflict=%d errors=%d · no automatic replay; verify the working tree before continuing",
		len(recs),
		counts[tools.MutationNotPublished],
		counts[tools.MutationPublished],
		counts[tools.MutationMissing],
		counts[tools.MutationConflict],
		errorsSeen,
	))
}
