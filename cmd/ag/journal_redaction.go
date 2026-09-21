package main

import (
	"encoding/json"

	"nabd/internal/agent"
	"nabd/internal/config"
	"nabd/internal/redact"
	"nabd/internal/store"
)

const journalRedactionEnv = "NABD_REDACT_JOURNAL"

// journalRedactionEnabled is fail-closed: journal redaction is on unless the
// operator explicitly opts out with the documented literal 0. Config v1 is
// resolved before the process environment, matching the other runtime
// settings. Config errors also keep the safer default.
func journalRedactionEnabled() bool {
	return config.Get(journalRedactionEnv) != "0"
}

// journalEventRedactor returns nil only for an explicit opt-out. A nil
// function lets storage retain its existing raw behavior without an
// unnecessary event copy.
func journalEventRedactor() func(agent.Event) agent.Event {
	if !journalRedactionEnabled() {
		return nil
	}
	return redactJournalEvent
}

// redactJournalEvent returns a redacted copy without mutating the event held in
// the live loop history. Structural identifiers, paths, hashes, blob addresses,
// decision values, and replay metadata remain unchanged.
func redactJournalEvent(e agent.Event) agent.Event {
	e.Text = redact.Redact(e.Text)
	e.Err = redact.Redact(e.Err)
	e.RawMessage = redact.Redact(e.RawMessage)
	e.RawRetryAfter = redact.Redact(e.RawRetryAfter)

	if e.Call != nil {
		call := *e.Call
		call.Args = json.RawMessage(
			redact.Redact(string(call.Args)),
		)
		call.Output = redact.Redact(call.Output)
		e.Call = &call
	}

	if e.Edit != nil {
		edit := *e.Edit
		edit.Patch = redact.Redact(edit.Patch)
		e.Edit = &edit
	}

	if e.Route != nil {
		route := *e.Route
		route.Reason = redact.Redact(route.Reason)
		e.Route = &route
	}

	return e
}

// journalStoreOptions converts the process-level policy into explicit storage
// options. Store itself never reads process environment variables.
func journalStoreOptions() store.Options {
	return store.Options{
		Redact: journalEventRedactor(),
	}
}

// openSessionJournal opens an existing journal for --continue using the same
// redaction policy as newly created journals.
func openSessionJournal(path string) (*store.JSONL, error) {
	return store.NewJSONLWithOptions(path, journalStoreOptions())
}
