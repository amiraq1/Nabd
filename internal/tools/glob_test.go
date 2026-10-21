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

// TestGlobStrictArgumentBoundary pins the decoder behind glob with repair
// disabled: this is the net behind RepairCall, so a bypass the repair layer
// happens to drop must still be refused on its own.
func TestGlobStrictArgumentBoundary(t *testing.T) {
	r, _ := newReg(t)
	r.repairOff = true

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"exact duplicate key", `{"pattern":"*","pattern":"*.go"}`, "duplicate key"},
		{"case-varied duplicate key", `{"pattern":"*","Pattern":"*.go"}`, "duplicate key"},
		{"escaped spelling of the same key", `{"pattern":"*","\u0070attern":"*.go"}`, "duplicate key"},
		{"undeclared field", `{"pattern":"*","bogus":1}`, "unknown field"},
		{"nesting past the ceiling", `{"pattern":"*","deep":` + nestedObject(maxJSONDepth+1) + `}`, "nest deeper than"},
		{"trailing object", `{"pattern":"*"}{"pattern":"x"}`, "unexpected trailing data"},
		{"trailing primitive", `{"pattern":"*"} 5`, "unexpected trailing data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, ok, err := r.Run(context.Background(), providerToolCall("glob", json.RawMessage(tc.raw)))
			if err == nil || ok {
				t.Fatalf("glob accepted %s: ok=%v err=%v out=%q", tc.name, ok, err, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("glob %s: error %q does not contain %q", tc.name, err, tc.want)
			}
		})
	}
}

// TestGlobArgumentsOnTheProductionPath pins the layering for glob, which is
// ReadOnly: the repair layer may drop a key the tool does not declare, and it
// must report the drop; a duplicate the tool does declare is refused by the
// decoder instead.
func TestGlobArgumentsOnTheProductionPath(t *testing.T) {
	t.Run("duplicate key rejected", func(t *testing.T) {
		r, _ := newReg(t)
		out, ok, err := r.Run(context.Background(), providerToolCall("glob", json.RawMessage(`{"pattern":"*","pattern":"*.go"}`)))
		if err == nil || ok {
			t.Fatalf("glob accepted a duplicate key: ok=%v err=%v out=%q", ok, err, out)
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("error %q does not contain %q", err, "duplicate key")
		}
	})

	t.Run("case-varied key is refused, not guessed", func(t *testing.T) {
		r, _ := newReg(t)
		out, ok, err := r.Run(context.Background(), providerToolCall("glob", json.RawMessage(`{"pattern":"*","Pattern":"*.go"}`)))
		if err == nil || ok {
			t.Fatalf("a case-varied duplicate was accepted: ok=%v err=%v out=%q", ok, err, out)
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("expected a duplicate-key refusal, got: %v", err)
		}
	})

	t.Run("undeclared field dropped and reported", func(t *testing.T) {
		r, _ := newReg(t)
		var fixes []Fix
		r.OnRepair = func(f Fix) { fixes = append(fixes, f) }
		defer func() { r.OnRepair = nil }()

		_, ok, err := r.Run(context.Background(), providerToolCall("glob", json.RawMessage(`{"pattern":"*","bogus":1}`)))
		if err != nil || !ok {
			t.Fatalf("glob failed: ok=%v err=%v", ok, err)
		}
		if got := DroppedKeys(fixes); len(got) != 1 || got[0] != "bogus" {
			t.Fatalf("the dropped key must be reported, got fixes=%+v dropped=%v", fixes, got)
		}
	})

	t.Run("duplicate plus an undeclared key is refused, not repaired", func(t *testing.T) {
		// Repair re-marshals the object after dropping the undeclared key, and
		// that would silently keep only the last value of the repeated key:
		// the ambiguity has to reach the decoder instead.
		r, _ := newReg(t)
		var fixes []Fix
		r.OnRepair = func(f Fix) { fixes = append(fixes, f) }
		defer func() { r.OnRepair = nil }()

		out, ok, err := r.Run(context.Background(), providerToolCall("glob", json.RawMessage(`{"pattern":"a","pattern":"b","bogus":1}`)))
		if err == nil || ok {
			t.Fatalf("repair erased a duplicate key instead of leaving the refusal to the decoder: ok=%v err=%v out=%q fixes=%+v", ok, err, out, fixes)
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("error %q does not contain %q", err, "duplicate key")
		}
	})
}

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
