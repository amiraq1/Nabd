package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGlobStarMatchesRootFiles: a bare "*" must match every root-level
// file — including dotfiles — and must not cross a separator. A live
// session once saw ".goreleaser.yml" alone from `glob *`; on the current
// code that cannot reproduce: matching is proven here against a
// Go-repo-shaped fixture.
func TestGlobStarMatchesRootFiles(t *testing.T) {
	r, dir := newReg(t)
		// main.go would normally be newest, but the stamp below is
	// `now + (len(files)-i) minutes`, so files[0] (".goreleaser.yml")
	// is the newest; the list is emitted newest-first.
	files := []string{".goreleaser.yml", "go.mod", "README.md", "main.go"}
	for i, f := range files {
		p := filepath.Join(dir, f)
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Distinct mtimes for a deterministic newest-first order.
		stamp := time.Now().Add(time.Duration(len(files)-i) * time.Minute)
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "tools", "read.go"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(map[string]any{"pattern": "*"})
	out, ok, err := r.Run(context.Background(), providerToolCall("glob", raw))
	if err != nil || !ok {
		t.Fatalf("glob: ok=%v err=%v", ok, err)
	}
	for _, f := range files {
		if !strings.Contains(out, f+"\n") {
			t.Errorf("glob * must return %s, got:\n%s", f, out)
		}
	}
	if strings.Contains(out, "internal/tools/read.go") {
		t.Errorf("* must not match across separators, got:\n%s", out)
	}
		// Newest first: ".goreleaser.yml" carries the largest
	// (len(files)-0)-minute stamp and must lead the list.
	if !strings.HasPrefix(out, files[0]+"\n") {
		t.Errorf("newest file must be first, got:\n%s", out)
	}
}

// TestGlobOffsetPagination: a truncated list must carry the same
// continuation contract as read_file — the exact range shown and the
// offset that resumes after it. A silent cut invites the model to
// summarise half the truth.
func TestGlobOffsetPagination(t *testing.T) {
	r, dir := newReg(t)
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%d.txt", i))
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(time.Duration(5-i) * time.Minute) // f0 newest
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	run := func(args map[string]any) string {
		raw, _ := json.Marshal(args)
		out, ok, err := r.Run(context.Background(), providerToolCall("glob", raw))
		if err != nil || !ok {
			t.Fatalf("glob(%v): ok=%v err=%v", args, ok, err)
		}
		return out
	}

	out := run(map[string]any{"pattern": "*.txt", "limit": 2})
	if !strings.Contains(out, "f0.txt\n") || !strings.Contains(out, "f1.txt\n") {
		t.Errorf("page 1 must hold the two newest, got:\n%s", out)
	}
	if !strings.Contains(out, "showing 1-2 of 5 · continue with offset=3") {
		t.Errorf("page 1 tail must name range and next offset, got:\n%s", out)
	}

	out = run(map[string]any{"pattern": "*.txt", "limit": 2, "offset": 3})
	if !strings.Contains(out, "f2.txt\n") || !strings.Contains(out, "f3.txt\n") {
		t.Errorf("page 2 must resume at offset 3, got:\n%s", out)
	}
	if !strings.Contains(out, "showing 3-4 of 5 · continue with offset=5") {
		t.Errorf("page 2 tail wrong, got:\n%s", out)
	}

	out = run(map[string]any{"pattern": "*.txt", "limit": 2, "offset": 5})
	if !strings.Contains(out, "f4.txt\n") || strings.Contains(out, "continue with offset") {
		t.Errorf("last page must be complete and tail-free, got:\n%s", out)
	}

	out = run(map[string]any{"pattern": "*.txt", "offset": 9})
	if !strings.Contains(out, "no results at offset=9 · 5 total") {
		t.Errorf("offset past the end must say so explicitly, got:\n%s", out)
	}
}
