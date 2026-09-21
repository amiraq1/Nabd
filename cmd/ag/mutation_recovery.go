package main

import (
	"fmt"

	"nabd/internal/agent"
)

const unresolvedMutationNotice = "recovery: %d mutation intent(s) remain unresolved after the previous run; no automatic replay; verify the working tree before continuing"

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

func noteUnresolvedMutations(loop *agent.Loop, evs []agent.Event) {
	if n := unresolvedMutationIntents(evs); n > 0 {
		loop.Note(fmt.Sprintf(unresolvedMutationNotice, n))
	}
}
