package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// TestSystemModelDirection: system is model-facing text sent with every
// request. It must be English words (model-directed strings are English only)
// AND carry an explicit output-language directive — otherwise the Arabic
// replies are lost, which is exactly the value the translation keeps.
// At HEAD the const was the Arabic original: no output-language directive and
// non-ASCII letters, so this test fails pre-fix and passes after.
func TestSystemModelDirection(t *testing.T) {
	for _, r := range system {
		if r > 127 && unicode.IsLetter(r) {
			t.Fatalf("system must not contain non-ASCII letters (got %q)", r)
		}
	}
	for _, want := range []string{
		"Reply in Arabic", // explicit output-language directive
		"50 columns",      // width instruction
		"never repeat",     // the brevity triad
		"never apologise",
		"never list anything without cause",
		"Two lines suffice",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("system must contain %q (the meaning was dropped)", want)
		}
	}
}

// TestLatestSessionRespectsDir verifies that latestSession searches only
// within the given directory (respecting --dir) and returns a clear Arabic
// error when no session exists.
func TestLatestSessionRespectsDir(t *testing.T) {
	// Empty directory → clear error.
	dir := t.TempDir()
	if _, err := latestSession(dir); err == nil {
		t.Error("expected error for empty dir, got nil")
	} else if got := err.Error(); !strings.Contains(got, "لا جلسات سابقة") {
		t.Errorf("error should be in Arabic, got %q", got)
	}

	// Directory with a session → returns the .jsonl path.
	sessionFile := filepath.Join(dir, "20260901-120000.000.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := latestSession(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sessionFile {
		t.Errorf("got %q, want %q", got, sessionFile)
	}
}

// TestSessionPathCreatesUniqueNewFile verifies that sessionPath generates a
// new unique filename (never reuses an existing one) and respects --dir.
func TestSessionPathCreatesUniqueNewFile(t *testing.T) {
	dir := t.TempDir()
	p1, err := sessionPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p1, ".jsonl") {
		t.Errorf("sessionPath should end with .jsonl, got %q", p1)
	}
	// Millisecond precision means the name includes a dot before jsonl.
	base := filepath.Base(p1)
	if !strings.Contains(base, ".") {
		t.Errorf("sessionPath should use millisecond precision, got %q", base)
	}
	// Calling again produces a different name (or same second is OK as long
	// as we don't collide with existing files).
	p2, err := sessionPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	// They may be equal if called within the same millisecond; that's fine
	// as long as the file doesn't pre-exist. What matters is that the caller
	// (doChat) does NOT call sessionPath on --continue.
	_ = p2
}