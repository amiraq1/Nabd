package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestP03DeferredPathsRefusedWithoutApproval is the P0-3 (B2 / F-03) red-phase
// characterization test.
//
// The plan requires two protection levels for deferred write paths:
//   - strict reject:  .git/hooks/* and .git/config
//   - always-ask with diff (even under YOLO): Makefile, package.json,
//     .github/workflows/*, .envrc
//
// commit() cannot ask for approval; the gate must ask first and then pass a
// one-shot capability bound to the normalized relative path + operation + call
// id. YOLO does NOT create this capability and must not be read as consent. A
// deferred target reached without that capability must be refused.
//
// IMPORTANT: Registry.Run (registry.go:374) does not consult perm.Policy — the
// permission gate lives in the agent loop. So calling r.Run(write_file, …) here
// simulates "no approval token in ctx" directly: today commit() has no deferred
// guard and writes proceed; after B2 adds the guard (refusing any deferred path
// lacking the one-shot capability) these writes are refused. The perm mode / YOLO
// settings are therefore irrelevant at this boundary and are intentionally not
// configured.
//
// It currently FAILS (red) because no such guard exists. B2 implements it, at
// which point this test turns green.
func TestP03DeferredPathsRefusedWithoutApproval(t *testing.T) {
	r, _, dir := newGatedReg(t, "") // no .gitignore: refusal is solely the new P0-3 guard

	cases := []struct {
		name string
		path string
	}{
		{"git-hook", ".git/hooks/pre-commit"},
		{"git-config", ".git/config"},
		{"makefile", "Makefile"},
		{"package-json", "package.json"},
		{"github-workflow", ".github/workflows/ci.yml"},
		{"envrc", ".envrc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Ensure the target exists so the call is a real mutation of an
			// existing file, not a creation miss. Create parent dirs as needed
			// (e.g. .git/hooks, .github/workflows).
			full := filepath.Join(dir, tc.path)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("existing-content\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			raw := json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"malicious-payload\n"}`, tc.path))
			out, ok, err := r.Run(context.Background(), providerToolCall("write_file", raw))
			if ok {
				t.Fatalf("write_file to deferred path %q must be refused, got ok=true out=%q", tc.path, out)
			}
			if err == nil {
				t.Fatalf("write_file to deferred path %q must return an error, got nil (vulnerability: deferred path was written without approval)", tc.path)
			}
		})
	}
}
