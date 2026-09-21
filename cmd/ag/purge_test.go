package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPurgeIsDryRunUnlessConfirmed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("sensitive\n"), 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	var out, errOut bytes.Buffer
	if code := runPurgeCommand([]string{"--dir", dir}, &out, &errOut); code != 0 {
		t.Fatalf("dry-run exit code=%d stderr=%q", code, errOut.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dry-run removed journal: %v", err)
	}
	if !strings.Contains(out.String(), "re-run with --yes") {
		t.Fatalf("dry-run did not explain confirmation: %q", out.String())
	}
}

func TestPurgeDeletesOnlyEligibleRegularJournals(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.jsonl")
	recent := filepath.Join(dir, "recent.jsonl")
	other := filepath.Join(dir, "notes.txt")
	subdir := filepath.Join(dir, "nested.jsonl")
	for path, data := range map[string]string{
		old:    "old\n",
		recent: "recent\n",
		other:  "keep\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(old, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	var out, errOut bytes.Buffer
	cutoff := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if code := runPurgeCommand([]string{"--dir", dir, "--before", cutoff, "--yes"}, &out, &errOut); code != 0 {
		t.Fatalf("purge exit code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old journal still exists: %v", err)
	}
	for _, path := range []string{recent, other, subdir, link} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("ineligible path %s changed: %v", path, err)
		}
	}
	if !strings.Contains(out.String(), "purged 1 session journal") {
		t.Fatalf("unexpected purge output: %q", out.String())
	}
}

func TestPurgeRejectsInvalidCutoff(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runPurgeCommand([]string{"--dir", t.TempDir(), "--before", "tomorrow"}, &out, &errOut); code != 2 {
		t.Fatalf("exit code=%d, want 2; stderr=%q", code, errOut.String())
	}
}
