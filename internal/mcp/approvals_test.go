package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func testExecIdentity(t *testing.T) ExecIdentity {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "srvbin")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := ComputeExecIdentity(bin, []string{"--mcp"}, []string{"B_VAR", "A_VAR"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestApprovalStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	exec := testExecIdentity(t)
	if s.Check("srv", "tool", "fp1", exec) {
		t.Fatal("unknown approval should not check")
	}
	if err := s.Record(Approval{
		Server: "srv", Tool: "tool", Fingerprint: "fp1",
		Classification: "read-only", Exec: exec,
	}); err != nil {
		t.Fatal(err)
	}
	if !s.Check("srv", "tool", "fp1", exec) {
		t.Fatal("recorded approval should check")
	}
	// Persisted: reopen.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Check("srv", "tool", "fp1", exec) {
		t.Fatal("approval should survive reopen")
	}
	// File is 0600.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("approvals file mode = %o, want 600", fi.Mode().Perm())
	}
}

// TestMCPApprovalInvalidation: any change to the tool fingerprint or the
// executable identity invalidates the approval (MCP-SEC-06/07).
func TestMCPApprovalInvalidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	exec := testExecIdentity(t)
	if err := s.Record(Approval{
		Server: "srv", Tool: "tool", Fingerprint: "fp1",
		Classification: "write", Exec: exec,
	}); err != nil {
		t.Fatal(err)
	}

	changedFP := "fp2"
	if s.Check("srv", "tool", changedFP, exec) {
		t.Fatal("fingerprint change must invalidate approval")
	}

	changedExec := exec
	changedExec.Args = []string{"--mcp", "--evil"}
	if s.Check("srv", "tool", "fp1", changedExec) {
		t.Fatal("argv change must invalidate approval")
	}

	changedExec2 := exec
	changedExec2.BinarySHA256 = "00"
	if s.Check("srv", "tool", "fp1", changedExec2) {
		t.Fatal("binary change must invalidate approval")
	}

	changedExec3 := exec
	changedExec3.EnvNames = []string{"OTHER"}
	if s.Check("srv", "tool", "fp1", changedExec3) {
		t.Fatal("env scope change must invalidate approval")
	}

	if s.Check("srv", "other-tool", "fp1", exec) {
		t.Fatal("approval must not leak across tools")
	}
	if s.Check("other-srv", "tool", "fp1", exec) {
		t.Fatal("approval must not leak across servers")
	}
}

func TestApprovalStoreCorruptFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("corrupt approvals file must fail closed, not start empty")
	}
}

func TestComputeExecIdentity(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "b")
	content := []byte("binary-content")
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := ComputeExecIdentity(bin, []string{"x"}, []string{"Z", "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ComputeExecIdentity(bin, []string{"x"}, []string{"A", "Z"})
	if err != nil {
		t.Fatal(err)
	}
	if a.BinarySHA256 != b.BinarySHA256 || len(a.EnvNames) != 2 || a.EnvNames[0] != "A" {
		t.Fatalf("identity not canonical: %+v", a)
	}
	if len(a.BinarySHA256) != 64 {
		t.Fatalf("not a sha256 hex: %q", a.BinarySHA256)
	}
	if _, err := ComputeExecIdentity(filepath.Join(dir, "missing"), nil, nil); err == nil {
		t.Fatal("expected error for missing executable")
	}
}

// TestApprovalStoreSymlinkRefused: the approval store refuses symlinks
// (fail-closed) — a symlinked store could redirect approvals elsewhere.
func TestApprovalStoreSymlinkRefused(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	if err := os.WriteFile(real, []byte(`{"approvals":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(link); err == nil {
		t.Fatal("symlinked approval store must be refused")
	}

	// Writes are safe by construction: rename(2) over a symlink replaces the
	// link itself, never the target. Verify the link was replaced and the
	// target untouched.
	s, err := OpenStore(real)
	if err != nil {
		t.Fatal(err)
	}
	s.path = link // simulate a symlinked path
	if err := s.Record(Approval{Server: "s", Tool: "t", Fingerprint: "fp"}); err != nil {
		t.Fatalf("write over symlink: %v", err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("symlink must have been replaced by a regular file")
	}
	if raw, _ := os.ReadFile(real); string(raw) != `{"approvals":{}}` {
		t.Fatalf("target file must be untouched, got %q", raw)
	}
}

// TestJournalSymlinkRefused: the journal append refuses symlinks.
func TestJournalSymlinkRefused(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real.jsonl")
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(link, filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	if j == nil {
		t.Skip("journal disabled")
	}
	if err := j.Append(JournalEvent{Kind: "test"}); err == nil {
		t.Fatal("symlinked journal append must be refused")
	}
}
