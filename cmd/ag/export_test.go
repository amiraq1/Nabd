package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/redact"
)

func writeRawJournal(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	return path
}

// 1. Raw export is byte-for-byte identical to the source.
func TestExportRawIsByteIdentical(t *testing.T) {
	const content = "{\"seq\":1,\"type\":\"run_start\",\"project_root\":\"/p\",\"future\":\"kept\"}\n\n{\"seq\":2,\"type\":\"user_msg\",\"text\":\"hi\"}"
	path := writeRawJournal(t, content)

	var out, errOut bytes.Buffer
	if err := exportJournal(path, false, &out, &errOut); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}
	if out.String() != content {
		t.Fatalf("raw export differs:\n got %q\nwant %q", out.String(), content)
	}
}

// 2. Raw export warns on stderr, never on stdout.
func TestExportRawWarnsOnStderrOnly(t *testing.T) {
	path := writeRawJournal(t, "{\"seq\":1,\"type\":\"run_start\"}\n")

	var out, errOut bytes.Buffer
	if err := exportJournal(path, false, &out, &errOut); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}
	if !strings.Contains(errOut.String(), rawExportWarning) {
		t.Fatalf("stderr missing raw warning: %q", errOut.String())
	}
	if strings.Contains(out.String(), "warning") {
		t.Fatalf("warning leaked to stdout: %q", out.String())
	}
}

// 3. Redacted export removes the credential and uses the replacement token.
func TestExportRedactedRemovesCredential(t *testing.T) {
	const secret = "sk-ant-abcdefgh12345678"
	line := `{"seq":1,"type":"user_msg","text":"token ` + secret + `"}`
	path := writeRawJournal(t, line+"\n")

	var out, errOut bytes.Buffer
	if err := exportJournal(path, true, &out, &errOut); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}
	if strings.Contains(out.String(), secret) {
		t.Fatalf("redacted export retained credential: %q", out.String())
	}
	if !strings.Contains(out.String(), redact.Token) {
		t.Fatalf("redacted export missing token: %q", out.String())
	}
}

// 4. Redacted export leaves the source event untouched.
func TestExportRedactedDoesNotChangeSourceEvent(t *testing.T) {
	const secret = "sk-or-abcdefgh12345678"
	line := `{"seq":1,"type":"user_msg","text":"` + secret + `"}`
	path := writeRawJournal(t, line+"\n")

	var out, errOut bytes.Buffer
	if err := exportJournal(path, true, &out, &errOut); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if !strings.Contains(string(after), secret) {
		t.Fatalf("source was mutated: %q", string(after))
	}
}

// 5. Source bytes, mode, and ModTime are unchanged by either export mode.
func TestExportLeavesSourceUntouched(t *testing.T) {
	const secret = "gsk_abcdefgh12345678"
	path := writeRawJournal(t, `{"seq":1,"type":"user_msg","text":"`+secret+`"}`+"\n")

	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	for _, redactOutput := range []bool{false, true} {
		var out, errOut bytes.Buffer
		if err := exportJournal(path, redactOutput, &out, &errOut); err != nil {
			t.Fatalf("exportJournal(redact=%v): %v", redactOutput, err)
		}
	}

	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}

	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatal("source bytes changed")
	}
	if beforeInfo.Mode() != afterInfo.Mode() {
		t.Fatalf("source mode changed: %v -> %v", beforeInfo.Mode(), afterInfo.Mode())
	}
	if !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("source ModTime changed: %v -> %v", beforeInfo.ModTime(), afterInfo.ModTime())
	}
}

// 6. No extra files appear next to the source.
func TestExportCreatesNoSiblingFiles(t *testing.T) {
	path := writeRawJournal(t, "{\"seq\":1,\"type\":\"user_msg\",\"text\":\"x\"}\n")
	dir := filepath.Dir(path)

	entriesBefore, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	var out, errOut bytes.Buffer
	if err := exportJournal(path, true, &out, &errOut); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}

	entriesAfter, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir after: %v", err)
	}
	if len(entriesAfter) != len(entriesBefore) {
		t.Fatalf("sibling file count changed: %d -> %d", len(entriesBefore), len(entriesAfter))
	}
}

// 7. Export redaction is independent of NABD_REDACT_JOURNAL.
func TestExportRedactionIndependentOfEnv(t *testing.T) {
	const secret = "nvapi-abcdefgh12345678"
	line := `{"seq":1,"type":"user_msg","text":"` + secret + `"}`
	path := writeRawJournal(t, line+"\n")

	t.Setenv(journalRedactionEnv, "")
	var redactedOut, redactedErr bytes.Buffer
	if err := exportJournal(path, true, &redactedOut, &redactedErr); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}
	if strings.Contains(redactedOut.String(), secret) {
		t.Fatalf("--redact ignored when env unset: %q", redactedOut.String())
	}

	t.Setenv(journalRedactionEnv, "1")
	var rawOut, rawErr bytes.Buffer
	if err := exportJournal(path, false, &rawOut, &rawErr); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}
	if !strings.Contains(rawOut.String(), secret) {
		t.Fatalf("raw export redacted because env was set: %q", rawOut.String())
	}
}

// 8. Unknown JSON fields survive raw export and are dropped by redacted export.
func TestExportUnknownFieldsRawVersusRedacted(t *testing.T) {
	line := `{"seq":1,"type":"user_msg","text":"hi","future_field":"x"}`
	path := writeRawJournal(t, line+"\n")

	var rawOut, rawErr bytes.Buffer
	if err := exportJournal(path, false, &rawOut, &rawErr); err != nil {
		t.Fatalf("raw export: %v", err)
	}
	if !strings.Contains(rawOut.String(), "future_field") {
		t.Fatalf("raw export dropped unknown field: %q", rawOut.String())
	}

	var redactedOut, redactedErr bytes.Buffer
	if err := exportJournal(path, true, &redactedOut, &redactedErr); err != nil {
		t.Fatalf("redacted export: %v", err)
	}
	if strings.Contains(redactedOut.String(), "future_field") {
		t.Fatalf("redacted export kept unknown field: %q", redactedOut.String())
	}
}

// 9. A truncated final line is preserved raw and ignored by redacted export.
func TestExportTruncatedFinalLine(t *testing.T) {
	content := "{\"seq\":1,\"type\":\"user_msg\",\"text\":\"ok\"}\n" + `{"seq":2,"type":`
	path := writeRawJournal(t, content)

	var rawOut, rawErr bytes.Buffer
	if err := exportJournal(path, false, &rawOut, &rawErr); err != nil {
		t.Fatalf("raw export: %v", err)
	}
	if rawOut.String() != content {
		t.Fatalf("raw export dropped truncated tail: %q", rawOut.String())
	}

	var redactedOut, redactedErr bytes.Buffer
	if err := exportJournal(path, true, &redactedOut, &redactedErr); err != nil {
		t.Fatalf("redacted export: %v", err)
	}
	if got := strings.Count(strings.TrimRight(redactedOut.String(), "\n"), "\n") + 1; got != 1 {
		t.Fatalf("redacted export event count=%d, want 1: %q", got, redactedOut.String())
	}
}

// 10. Every conflicted flag combination is rejected.
func TestCheckExportFlags(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		redact   bool
		narg     int
		provided map[string]bool
		wantErr  bool
	}{
		{name: "export only", path: "s.jsonl", provided: map[string]bool{"export": true}},
		{name: "export with redact", path: "s.jsonl", redact: true, provided: map[string]bool{"export": true, "redact": true}},
		{name: "no export no redact", provided: nil},
		{name: "redact without export", redact: true, provided: map[string]bool{"redact": true}, wantErr: true},
		{name: "empty export path", path: "", provided: map[string]bool{"export": true}, wantErr: true},
		{name: "positional args", path: "s.jsonl", narg: 1, provided: map[string]bool{"export": true}, wantErr: true},
		{name: "export with version", path: "s.jsonl", provided: map[string]bool{"export": true, "version": true}, wantErr: true},
		{name: "export with dir", path: "s.jsonl", provided: map[string]bool{"export": true, "dir": true}, wantErr: true},
		{name: "export with replay", path: "s.jsonl", provided: map[string]bool{"export": true, "replay": true}, wantErr: true},
		{name: "export with speed", path: "s.jsonl", provided: map[string]bool{"export": true, "speed": true}, wantErr: true},
		{name: "export with continue", path: "s.jsonl", provided: map[string]bool{"export": true, "continue": true}, wantErr: true},
		{name: "export with feed", path: "s.jsonl", provided: map[string]bool{"export": true, "feed": true}, wantErr: true},
		{name: "export with feed-touch", path: "s.jsonl", provided: map[string]bool{"export": true, "feed-touch": true}, wantErr: true},
		{name: "export with p", path: "s.jsonl", provided: map[string]bool{"export": true, "p": true}, wantErr: true},
		{name: "export with json", path: "s.jsonl", provided: map[string]bool{"export": true, "json": true}, wantErr: true},
		{name: "export with max-turns", path: "s.jsonl", provided: map[string]bool{"export": true, "max-turns": true}, wantErr: true},
		{name: "export with permission-mode", path: "s.jsonl", provided: map[string]bool{"export": true, "permission-mode": true}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkExportFlags(tc.path, tc.redact, tc.narg, tc.provided)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// 11. Redacted stdout is JSONL only: valid JSON per line, no diagnostics.
func TestExportRedactedStdoutIsJSONLOnly(t *testing.T) {
	const secret = "sk-ant-abcdefgh12345678"
	path := writeRawJournal(t,
		`{"seq":1,"type":"run_start","project_root":"/p"}`+"\n"+
			`{"seq":2,"type":"user_msg","text":"`+secret+`"}`+"\n",
	)

	var out, errOut bytes.Buffer
	if err := exportJournal(path, true, &out, &errOut); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("line count=%d, want 2: %q", len(lines), out.String())
	}
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("line %d is not valid JSON: %q", i+1, line)
		}
	}
	if strings.Contains(out.String(), rawExportWarning) {
		t.Fatal("diagnostic text leaked onto stdout")
	}
}

// 12. Old journals with missing fields export cleanly in both modes.
func TestExportSupportsLegacyJournals(t *testing.T) {
	path := writeRawJournal(t, `{"seq":1,"type":"run_start","project_root":"/p"}`+"\n")

	for _, redactOutput := range []bool{false, true} {
		var out, errOut bytes.Buffer
		if err := exportJournal(path, redactOutput, &out, &errOut); err != nil {
			t.Fatalf("exportJournal(redact=%v): %v", redactOutput, err)
		}
		if strings.TrimSpace(out.String()) == "" {
			t.Fatalf("exportJournal(redact=%v) produced no output", redactOutput)
		}
	}
}

// Sanity: the redacted export path emits the same encode as jsonlStdout.
func TestExportRedactedMatchesJSONLSink(t *testing.T) {
	events := []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "token sk-ant-abcdefgh12345678"},
	}

	var want bytes.Buffer
	sink := jsonlStdout{w: &want, redact: redactJournalEvent}
	for _, e := range events {
		if err := sink.Emit(e); err != nil {
			t.Fatalf("sink.Emit: %v", err)
		}
	}

	path := writeRawJournal(t,
		`{"seq":1,"type":"user_msg","text":"token sk-ant-abcdefgh12345678"}`+"\n",
	)
	var got, errOut bytes.Buffer
	if err := exportJournal(path, true, &got, &errOut); err != nil {
		t.Fatalf("exportJournal: %v", err)
	}
	if got.String() != want.String() {
		t.Fatalf("export/--json divergence:\n got %q\nwant %q", got.String(), want.String())
	}
}
