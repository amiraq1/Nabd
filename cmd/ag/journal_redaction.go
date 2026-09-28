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
// unnecessary event copy. The exactKeys are the configured provider secrets
// collected at session start; pattern redaction alone cannot cover custom
// providers whose key format matches no known shape.
func journalEventRedactor(exactKeys []string) func(agent.Event) agent.Event {
	if !journalRedactionEnabled() {
		return nil
	}
	return func(e agent.Event) agent.Event {
		return redactJournalEvent(e, exactKeys)
	}
}

// redactJournalEvent returns a redacted copy without mutating the event held in
// the live loop history. Structural identifiers, paths, hashes, blob addresses,
// decision values, and replay metadata remain unchanged. Exact keys are
// redacted before pattern matching so custom-provider secrets are covered too.
func redactJournalEvent(e agent.Event, exactKeys []string) agent.Event {
	redactText := func(s string) string {
		return redact.Redact(redact.RedactExactKeys(s, exactKeys))
	}

	e.Text = redactText(e.Text)
	e.Err = redactText(e.Err)
	e.RawMessage = redactText(e.RawMessage)
	e.RawRetryAfter = redactText(e.RawRetryAfter)

	if e.Call != nil {
		call := *e.Call
		call.Args = json.RawMessage(
			redactText(string(call.Args)),
		)
		call.Output = redactText(call.Output)
		e.Call = &call
	}

	if e.Edit != nil {
		edit := *e.Edit
		edit.Patch = redactText(edit.Patch)
		e.Edit = &edit
	}

	if e.Route != nil {
		route := *e.Route
		route.Reason = redactText(route.Reason)
		e.Route = &route
	}

	if e.SkillBody != nil {
		body := *e.SkillBody
		body.Body = redactText(body.Body)
		e.SkillBody = &body
	}

	return e
}

// journalStoreOptions converts the process-level policy into explicit storage
// options. Store itself never reads process environment variables.
func journalStoreOptions(exactKeys []string) store.Options {
	return store.Options{
		Redact: journalEventRedactor(exactKeys),
	}
}

// openSessionJournal opens an existing journal for --continue using the same
// redaction policy as newly created journals.
func openSessionJournal(path string, exactKeys []string) (*store.JSONL, error) {
	return store.NewJSONLWithOptions(path, journalStoreOptions(exactKeys))
}
