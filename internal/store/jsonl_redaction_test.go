package store

import (
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
)

func TestNewJSONLDefaultPreservesRawEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw.jsonl")
	journal, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}

	const raw = "raw credential-like value"
	if err := journal.Append(agent.Event{
		Seq:  1,
		Type: agent.UserMsg,
		Text: raw,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count=%d, want 1", len(events))
	}
	if events[0].Text != raw {
		t.Fatalf("default constructor changed text: %q", events[0].Text)
	}
}

func TestNewJSONLWithOptionsPersistsRedactedCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redacted.jsonl")

	redactor := func(e agent.Event) agent.Event {
		e.Text = "[REDACTED]"
		if e.Call != nil {
			call := *e.Call
			call.Output = "[REDACTED]"
			e.Call = &call
		}
		return e
	}

	journal, err := NewJSONLWithOptions(path, Options{
		Redact: redactor,
	})
	if err != nil {
		t.Fatalf("NewJSONLWithOptions: %v", err)
	}

	original := agent.Event{
		Seq:  1,
		Type: agent.ToolEnd,
		Text: "raw text",
		Call: &agent.ToolCall{
			ID:     "call-1",
			Name:   "read_file",
			Output: "raw output",
			OK:     true,
		},
	}

	if err := journal.Append(original); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if original.Text != "raw text" {
		t.Fatalf("Append mutated original event text: %q", original.Text)
	}
	if original.Call.Output != "raw output" {
		t.Fatalf("Append mutated original ToolCall: %q", original.Call.Output)
	}

	events, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count=%d, want 1", len(events))
	}
	if events[0].Text != "[REDACTED]" {
		t.Fatalf("persisted text=%q", events[0].Text)
	}
	if events[0].Call == nil ||
		events[0].Call.Output != "[REDACTED]" {
		t.Fatalf("persisted call=%#v", events[0].Call)
	}
}

func TestNewJSONLExclusiveWithOptionsUsesRedactor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exclusive.jsonl")

	journal, err := NewJSONLExclusiveWithOptions(path, Options{
		Redact: func(e agent.Event) agent.Event {
			e.Err = "[REDACTED]"
			return e
		},
	})
	if err != nil {
		t.Fatalf("NewJSONLExclusiveWithOptions: %v", err)
	}

	if err := journal.Append(agent.Event{
		Seq:  1,
		Type: agent.RunError,
		Err:  "raw error",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 || events[0].Err != "[REDACTED]" {
		t.Fatalf("persisted events=%#v", events)
	}
}

func TestJSONLRedactorRunsBeforeForStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordering.jsonl")
	rawOutput := strings.Repeat("x", agent.MaxPersistedOutput+100)

	sawFullOutput := false
	journal, err := NewJSONLWithOptions(path, Options{
		Redact: func(e agent.Event) agent.Event {
			if e.Call != nil &&
				len(e.Call.Output) == len(rawOutput) {
				sawFullOutput = true
			}
			return e
		},
	})
	if err != nil {
		t.Fatalf("NewJSONLWithOptions: %v", err)
	}

	if err := journal.Append(agent.Event{
		Seq:  1,
		Type: agent.ToolEnd,
		Call: &agent.ToolCall{
			ID:     "call-1",
			Name:   "read_file",
			Output: rawOutput,
			OK:     true,
		},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !sawFullOutput {
		t.Fatal("redactor did not receive full pre-ForStore output")
	}
}
