package store

import (
	"nabd/internal/event"
)

// Emit makes the journal an agent.Sink. Append already applies ForStore.
func (j *JSONL) Emit(e event.Event) error { return j.Append(e) }

func (j *JSONL) JournalPath() string { return j.Path() }
