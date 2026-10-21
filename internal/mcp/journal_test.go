package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalHMAC(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(filepath.Join(dir, "j.jsonl"), filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	h1 := j.HMACArgs(json.RawMessage(`{"a":1}`))
	h2 := j.HMACArgs(json.RawMessage(`{"a":1}`))
	if h1 != h2 || len(h1) != 64 {
		t.Fatalf("HMAC not deterministic: %q", h1)
	}
	h3 := j.HMACArgs(json.RawMessage(`{"a":2}`))
	if h3 == h1 {
		t.Fatal("different args must give different HMAC")
	}
	// Key file is 0600.
	fi, err := os.Stat(filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o, want 600", fi.Mode().Perm())
	}
}

// TestMCPSec13JournalPrivacy: the journal records decisions, sizes, and the
// HMAC of arguments — never raw arguments or raw outputs.
func TestMCPSec13JournalPrivacy(t *testing.T) {
	dir := t.TempDir()
	jpath := filepath.Join(dir, "j.jsonl")
	j, err := OpenJournal(jpath, filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	secretArg := `{"password":"s3cr3t-value"}`
	secretOut := "output containing s3cr3t-value"
	if err := j.Append(JournalEvent{
		Kind: "call", Server: "srv", Tool: "t", Decision: "approved",
		ArgsHMAC:    j.HMACArgs(json.RawMessage(secretArg)),
		OutputBytes: len(secretOut), Status: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if strings.Contains(content, "s3cr3t-value") {
		t.Fatal("journal contains raw secret")
	}
	if !strings.Contains(content, `"decision":"approved"`) {
		t.Fatal("journal must record the decision")
	}
	if !strings.Contains(content, `"args_hmac":"`) {
		t.Fatal("journal must record the args HMAC")
	}
	var ev JournalEvent
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(content)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("journal line not strict JSON: %v", err)
	}
}

func TestJournalDisabled(t *testing.T) {
	j, err := OpenJournal("", "")
	if err != nil || j != nil {
		t.Fatalf("empty paths must disable the journal: %v %v", err, j)
	}
	var nilJ *Journal
	if err := nilJ.Append(JournalEvent{Kind: "call"}); err != nil {
		t.Fatalf("append on nil journal must be a no-op: %v", err)
	}
}
