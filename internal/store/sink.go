package store

import "nabd/internal/agent"

// Emit makes the journal an agent.Sink. Append already applies ForStore.
func (j *JSONL) Emit(e agent.Event) error { return j.Append(e) }
