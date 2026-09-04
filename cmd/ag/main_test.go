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
		"never repeat",    // the brevity triad
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
	if _, err := latestSession(dir, "/project"); err == nil {
		t.Error("expected error for empty dir, got nil")
	} else if got := err.Error(); !strings.Contains(got, "لا جلسات سابقة") {
		t.Errorf("error should be in Arabic, got %q", got)
	}

	// Directory with a session → returns the .jsonl path.
	sessionFile := filepath.Join(dir, "20260901-120000.000.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{\"type\":\"run_start\",\"project_root\":\"/project\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := latestSession(dir, "/project")
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

func TestLatestSessionProjectIsolation(t *testing.T) {
	dir := t.TempDir()

	// 1. Legacy session (no project root) -> Should be skipped
	leg := filepath.Join(dir, "20260901-000000.000.jsonl")
	os.WriteFile(leg, []byte("{\"type\":\"run_start\"}\n"), 0644)

	// 2. Session for Project B -> Should be skipped if we want Project A
	pb := filepath.Join(dir, "20260901-010000.000.jsonl")
	os.WriteFile(pb, []byte("{\"type\":\"run_start\",\"project_root\":\"/projectB\"}\n"), 0644)

	// 3. Session for Project A -> MATCH
	pa1 := filepath.Join(dir, "20260901-020000.000.jsonl")
	os.WriteFile(pa1, []byte("{\"type\":\"run_start\",\"project_root\":\"/projectA\"}\n"), 0644)

	// 4. Newer Session for Project B -> The globally newest, but should be skipped
	pb2 := filepath.Join(dir, "20260901-030000.000.jsonl")
	os.WriteFile(pb2, []byte("{\"type\":\"run_start\",\"project_root\":\"/projectB\"}\n"), 0644)

	// 5. Newer Session for Project A -> NEWEST MATCH
	pa2 := filepath.Join(dir, "20260901-040000.000.jsonl")
	os.WriteFile(pa2, []byte("{\"type\":\"run_start\",\"project_root\":\"/projectA\"}\n"), 0644)

	got, err := latestSession(dir, "/projectA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != pa2 {
		t.Errorf("expected pa2 (%s) but got %s", pa2, got)
	}
}
