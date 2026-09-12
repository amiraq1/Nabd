//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grep walks with Lstat, so a symlink inside the root is a plain entry whose
// os.Open would follow it and print the target's content under a root-relative
// path. It must be skipped, not surfaced.
func TestGrepNeverSurfacesSymlinkedEntry(t *testing.T) {
	r, dir := newReg(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("OUTSIDE-CANARY-VALUE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("ordinary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolSymlink(t, secret, filepath.Join(dir, "inside-link.txt"))

	// The pattern is a prefix of the content, so the "no match · <pattern>"
	// footer cannot be mistaken for a match; only reading the target would put
	// the full value in the output.
	raw, _ := json.Marshal(map[string]any{"pattern": "OUTSIDE-CANARY"})
	out, ok, err := r.Run(context.Background(), providerToolCall("grep", raw))
	if err != nil || !ok {
		t.Fatalf("grep: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "OUTSIDE-CANARY-VALUE") {
		t.Errorf("grep followed a symlink out of root:\n%s", out)
	}
}

// A single-file grep path that is a symlink to outside the root must be
// refused, never read.
func TestGrepSingleFileRefusesSymlinkEscape(t *testing.T) {
	r, dir := newReg(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("SINGLE-CANARY-VALUE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolSymlink(t, secret, filepath.Join(dir, "link.txt"))

	raw, _ := json.Marshal(map[string]any{"pattern": "SINGLE-CANARY", "path": "link.txt"})
	out, ok, err := r.Run(context.Background(), providerToolCall("grep", raw))
	if err == nil && ok && strings.Contains(out, "SINGLE-CANARY-VALUE") {
		t.Errorf("grep followed a single-file symlink out of root:\n%s", out)
	}
}

// glob must list regular files only: a symlink advertised here becomes a path
// the model hands to read_file, and such a path is refused by the descriptor
// walk anyway.
func TestGlobOmitsSymlinkedEntries(t *testing.T) {
	r, dir := newReg(t)
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolSymlink(t, filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt"))

	raw, _ := json.Marshal(map[string]any{"pattern": "**/*.txt"})
	out, ok, err := r.Run(context.Background(), providerToolCall("glob", raw))
	if err != nil || !ok {
		t.Fatalf("glob: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "link.txt") {
		t.Errorf("glob listed a symlink:\n%s", out)
	}
	if !strings.Contains(out, "real.txt") {
		t.Errorf("glob dropped a regular file:\n%s", out)
	}
}

// The shadow store holds every historical version of every file. A traversal
// tool that walks .ag would read deleted content back out.
func TestTraversalToolsNeverSurfaceShadowStore(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	const canary = "SHADOW-STORE-CANARY-9f3a"
	raw, _ := json.Marshal(map[string]any{"path": "notes.txt", "content": canary + "\n"})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}
	// Remove the project file so the only copy left is the shadow blob.
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}

	globRaw, _ := json.Marshal(map[string]any{"pattern": "**/*"})
	gout, ok, err := r.Run(ctx, providerToolCall("glob", globRaw))
	if err != nil || !ok {
		t.Fatalf("glob: ok=%v err=%v", ok, err)
	}
	if strings.Contains(gout, ".ag/") || strings.Contains(gout, ".ag\n") {
		t.Errorf("glob surfaced the shadow store:\n%s", gout)
	}

	grepRaw, _ := json.Marshal(map[string]any{"pattern": "SHADOW-STORE-CANARY"})
	out, ok, err := r.Run(ctx, providerToolCall("grep", grepRaw))
	if err != nil || !ok {
		t.Fatalf("grep: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "SHADOW-STORE-CANARY-9f3a") {
		t.Errorf("grep read deleted content out of the shadow store:\n%s", out)
	}
}
