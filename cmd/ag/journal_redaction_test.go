package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/redact"
	"nabd/internal/store"
)

func TestJournalRedactionEnabledOnlyForLiteralOne(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"true", false},
		{"TRUE", false},
		{"yes", false},
		{" 1 ", false},
		{"1", true},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(journalRedactionEnv, tc.value)
			if got := journalRedactionEnabled(); got != tc.want {
				t.Fatalf(
					"journalRedactionEnabled()=%v, want %v for %q",
					got,
					tc.want,
					tc.value,
				)
			}
		})
	}
}

func TestJournalEventRedactorDisabledByDefault(t *testing.T) {
	t.Setenv(journalRedactionEnv, "")
	if got := journalEventRedactor(); got != nil {
		t.Fatal("disabled journal redactor must be nil")
	}
}

func TestRedactJournalEventCopiesAndRedactsSensitiveFields(t *testing.T) {
	const secret = "sk-ant-abcdefgh12345678"

	args := json.RawMessage(
		`{"authorization":"` + secret + `","path":"safe.txt"}`,
	)

	original := agent.Event{
		ProjectRoot:   "/project/" + secret,
		Text:          "user text " + secret,
		Err:           "provider error " + secret,
		ErrorCode:     "stable-" + secret,
		JournalPath:   "/sessions/" + secret + ".jsonl",
		RawMessage:    `{"error":"` + secret + `"}`,
		RawRetryAfter: "retry " + secret,
		Call: &agent.ToolCall{
			ID:     "call-" + secret,
			Name:   "tool-" + secret,
			Args:   args,
			Output: "tool output " + secret,
			Signal: "SIGTERM-" + secret,
		},
		Read: &agent.ReadRecord{
			Path: "/read/" + secret,
		},
		Edit: &agent.EditRecord{
			Path:       "/edit/" + secret,
			HashBefore: "before-" + secret,
			HashAfter:  "after-" + secret,
			BlobBefore: "blob-before-" + secret,
			BlobAfter:  "blob-after-" + secret,
			Patch:      "patch " + secret,
		},
		Route: &agent.ProviderRoute{
			StreamID: "stream-" + secret,
			Provider: "provider-" + secret,
			Model:    "model-" + secret,
			Status:   "status-" + secret,
			Reason:   "route error " + secret,
		},
	}

	got := redactJournalEvent(original)

	for name, value := range map[string]string{
		"Text":          got.Text,
		"Err":           got.Err,
		"RawMessage":    got.RawMessage,
		"RawRetryAfter": got.RawRetryAfter,
		"Call.Args":     string(got.Call.Args),
		"Call.Output":   got.Call.Output,
		"Edit.Patch":    got.Edit.Patch,
		"Route.Reason":  got.Route.Reason,
	} {
		if strings.Contains(value, secret) {
			t.Errorf("%s retained credential: %q", name, value)
		}
		if !strings.Contains(value, redact.Token) {
			t.Errorf("%s lacks replacement token: %q", name, value)
		}
	}

	if !json.Valid(got.Call.Args) {
		t.Fatalf("redacted tool arguments are invalid JSON: %q", got.Call.Args)
	}

	// Fields required for replay, resume, undo, and event identity must remain
	// byte-for-byte unchanged even if they resemble a credential.
	for name, pair := range map[string][2]string{
		"ProjectRoot": {
			original.ProjectRoot,
			got.ProjectRoot,
		},
		"ErrorCode": {
			original.ErrorCode,
			got.ErrorCode,
		},
		"JournalPath": {
			original.JournalPath,
			got.JournalPath,
		},
		"Call.ID": {
			original.Call.ID,
			got.Call.ID,
		},
		"Call.Name": {
			original.Call.Name,
			got.Call.Name,
		},
		"Call.Signal": {
			original.Call.Signal,
			got.Call.Signal,
		},
		"Read.Path": {
			original.Read.Path,
			got.Read.Path,
		},
		"Edit.Path": {
			original.Edit.Path,
			got.Edit.Path,
		},
		"Edit.HashBefore": {
			original.Edit.HashBefore,
			got.Edit.HashBefore,
		},
		"Edit.HashAfter": {
			original.Edit.HashAfter,
			got.Edit.HashAfter,
		},
		"Edit.BlobBefore": {
			original.Edit.BlobBefore,
			got.Edit.BlobBefore,
		},
		"Edit.BlobAfter": {
			original.Edit.BlobAfter,
			got.Edit.BlobAfter,
		},
		"Route.StreamID": {
			original.Route.StreamID,
			got.Route.StreamID,
		},
		"Route.Provider": {
			original.Route.Provider,
			got.Route.Provider,
		},
		"Route.Model": {
			original.Route.Model,
			got.Route.Model,
		},
		"Route.Status": {
			original.Route.Status,
			got.Route.Status,
		},
	} {
		if pair[1] != pair[0] {
			t.Errorf(
				"%s changed: got %q want %q",
				name,
				pair[1],
				pair[0],
			)
		}
	}

	// Verify copy-on-write: live history pointers and strings stay raw.
	if !strings.Contains(original.Call.Output, secret) {
		t.Fatal("redactor mutated original ToolCall")
	}
	if !strings.Contains(string(original.Call.Args), secret) {
		t.Fatal("redactor mutated original ToolCall.Args")
	}
	if !strings.Contains(original.Edit.Patch, secret) {
		t.Fatal("redactor mutated original EditRecord")
	}
	if !strings.Contains(original.Route.Reason, secret) {
		t.Fatal("redactor mutated original ProviderRoute")
	}

	if got.Call == original.Call {
		t.Fatal("redactor reused ToolCall pointer")
	}
	if got.Edit == original.Edit {
		t.Fatal("redactor reused EditRecord pointer")
	}
	if got.Route == original.Route {
		t.Fatal("redactor reused ProviderRoute pointer")
	}
}

func TestJournalEventRedactorEnabled(t *testing.T) {
	t.Setenv(journalRedactionEnv, "1")

	fn := journalEventRedactor()
	if fn == nil {
		t.Fatal("enabled journal redactor is nil")
	}

	got := fn(agent.Event{
		Text: "token sk-or-abcdefgh12345678",
	})
	if strings.Contains(got.Text, "sk-or-abcdefgh12345678") {
		t.Fatalf("enabled redactor retained credential: %q", got.Text)
	}
}

func TestOpenSessionJournalUsesEnabledRedaction(t *testing.T) {
	t.Setenv(journalRedactionEnv, "1")

	path := filepath.Join(t.TempDir(), "continued.jsonl")
	journal, err := openSessionJournal(path)
	if err != nil {
		t.Fatalf("openSessionJournal: %v", err)
	}

	const secret = "sk-or-abcdefgh12345678"
	if err := journal.Append(agent.Event{
		Seq:  1,
		Type: agent.UserMsg,
		Text: "continued " + secret,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := store.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count=%d, want 1", len(events))
	}
	if strings.Contains(events[0].Text, secret) {
		t.Fatalf("continued journal retained credential: %q", events[0].Text)
	}
}

func TestNewSessionJournalUsesEnabledRedactionAndWarning(t *testing.T) {
	t.Setenv(journalRedactionEnv, "1")

	var warnings bytes.Buffer
	journal, path, err := newSessionJournalWithWarning(
		t.TempDir(),
		&warnings,
	)
	if err != nil {
		t.Fatalf("newSessionJournalWithWarning: %v", err)
	}

	const secret = "gsk_abcdefgh12345678"
	if err := journal.Append(agent.Event{
		Seq:  1,
		Type: agent.UserMsg,
		Text: secret,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if warnings.String() != redactedJournalWarning {
		t.Fatalf("warning=%q, want %q",
			warnings.String(),
			redactedJournalWarning,
		)
	}

	events, err := store.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 ||
		strings.Contains(events[0].Text, secret) {
		t.Fatalf("persisted events=%#v", events)
	}
}

func TestJSONLStdoutFollowsEnabledJournalRedaction(t *testing.T) {
	const secret = "nvapi-abcdefgh12345678"

	var stdout bytes.Buffer
	sink := jsonlStdout{
		w:      &stdout,
		redact: redactJournalEvent,
	}

	original := agent.Event{
		Seq:  1,
		Type: agent.ToolEnd,
		Call: &agent.ToolCall{
			ID:     "call-1",
			Name:   "read_file",
			Output: secret,
			OK:     true,
		},
	}

	if err := sink.Emit(original); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	var persisted agent.Event
	if err := json.Unmarshal(stdout.Bytes(), &persisted); err != nil {
		t.Fatalf("stdout is not valid journal JSONL: %v; %q",
			err,
			stdout.String(),
		)
	}

	if persisted.Call == nil {
		t.Fatal("persisted call is nil")
	}
	if strings.Contains(persisted.Call.Output, secret) {
		t.Fatalf("--json retained credential: %q", persisted.Call.Output)
	}
	if persisted.Call.Output != redact.Token {
		t.Fatalf("--json output=%q, want %q",
			persisted.Call.Output,
			redact.Token,
		)
	}

	if original.Call.Output != secret {
		t.Fatalf("jsonlStdout mutated live event: %q",
			original.Call.Output,
		)
	}
}
