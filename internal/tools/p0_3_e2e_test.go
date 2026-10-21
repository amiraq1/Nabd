package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// P0-3 end-to-end: write_file must refuse .git/hooks paths.
func TestP03_WriteFileRefusesGitHooks(t *testing.T) {
	r, _, dir := newGatedReg(t, "dist/\n")

	// Ensure .git/hooks exists so the write would succeed without the guard.
	hooksDir := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	out, ok, err := r.Run(context.Background(), providerToolCall("write_file", json.RawMessage(`{"path":".git/hooks/pre-commit","content":"#!/bin/sh\necho pwned\n"}`)))
	if ok || err == nil {
		t.Fatalf("write_file to .git/hooks/pre-commit must fail: ok=%v err=%v out=%q", ok, err, out)
	}

	// File must not exist on disk.
	if _, statErr := os.Stat(filepath.Join(hooksDir, "pre-commit")); !os.IsNotExist(statErr) {
		t.Fatal("write_file must not create .git/hooks/pre-commit")
	}
}

// P0-3: write_file must still allow excluded build paths (dist/).
func TestP03_WriteFileAllowsExcludedDist(t *testing.T) {
	r, _, dir := newGatedReg(t, "dist/\n")

	out, ok, err := r.Run(context.Background(), providerToolCall("write_file", json.RawMessage(`{"path":"dist/bundle.js","content":"console.log(1);\n"}`)))
	if !ok || err != nil {
		t.Fatalf("write_file to dist/bundle.js must succeed: ok=%v err=%v out=%q", ok, err, out)
	}
	if _, rerr := os.ReadFile(filepath.Join(dir, "dist", "bundle.js")); rerr != nil {
		t.Fatalf("file not written: %v", rerr)
	}
}
