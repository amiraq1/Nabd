package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileRefusesGitConfigWithErrProtected is the P0-2 (F-02) red-phase
// characterization test.
//
// It proves the security property the plan requires: reading a file inside a
// .git directory must be refused with errors.Is(err, ErrProtected),
// independent of any .gitignore, and the file content must never be returned.
//
// It currently FAILS (red) because the read path does not yet hard-block .git/.ag
// components — only .gitignore-based gating exists today. The enforcement that
// returns ErrProtected is implemented in B1 (F-02); once it lands this test
// turns green. It is written now so the gap is pinned by a regression test
// rather than by prose.
func TestReadFileRefusesGitConfigWithErrProtected(t *testing.T) {
	r, dir := newReg(t)
	gitConfig := filepath.Join(dir, ".git", "config")
	if err := os.MkdirAll(filepath.Dir(gitConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "super-secret-git-config-content\n"
	if err := os.WriteFile(gitConfig, []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}

	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":".git/config"}`)))
	if ok {
		t.Fatalf("read_file must refuse .git/config, got ok=true out=%q", out)
	}
	if err == nil {
		t.Fatal("read_file must return an error for .git/config, got nil (vulnerability: protected path was read)")
	}
	if !errors.Is(err, ErrProtected) {
		t.Fatalf("refusal of .git/config must be errors.Is(err, ErrProtected); got: %v", err)
	}
	if strings.Contains(out+err.Error(), secret) {
		t.Fatalf("refused read leaked .git/config content: out=%q err=%v", out, err)
	}
}

// TestReadFileRefusesAgShadowWithErrProtected mirrors the .git case for the
// .ag shadow store: .ag holds every historical version of every file, so a read
// into it must be refused with ErrProtected and must never return content.
func TestReadFileRefusesAgShadowWithErrProtected(t *testing.T) {
	r, dir := newReg(t)
	shadowPath := filepath.Join(dir, ".ag", "shadow", "leaked.txt")
	if err := os.MkdirAll(filepath.Dir(shadowPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "super-secret-shadow-content\n"
	if err := os.WriteFile(shadowPath, []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}

	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":".ag/shadow/leaked.txt"}`)))
	if ok {
		t.Fatalf("read_file must refuse .ag/shadow paths, got ok=true out=%q", out)
	}
	if err == nil {
		t.Fatal("read_file must return an error for .ag/shadow, got nil (vulnerability: shadow store was read)")
	}
	if !errors.Is(err, ErrProtected) {
		t.Fatalf("refusal of .ag/shadow must be errors.Is(err, ErrProtected); got: %v", err)
	}
	if strings.Contains(out+err.Error(), secret) {
		t.Fatalf("refused read leaked .ag/shadow content: out=%q err=%v", out, err)
	}
}

// TestReadFileRefusesGitConfigCaseVariantWithErrProtected pins the
// case-insensitivity requirement from the plan: a component spelled with
// different case (e.g. .GIT/config) must also be refused, so the block cannot
// be escaped by case variation on case-preserving, case-insensitive
// filesystems.
func TestReadFileRefusesGitConfigCaseVariantWithErrProtected(t *testing.T) {
	r, dir := newReg(t)
	gitConfig := filepath.Join(dir, ".GIT", "config")
	if err := os.MkdirAll(filepath.Dir(gitConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "super-secret-git-config-content\n"
	if err := os.WriteFile(gitConfig, []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}

	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":".GIT/config"}`)))
	if ok {
		t.Fatalf("read_file must refuse .GIT/config, got ok=true out=%q", out)
	}
	if err == nil {
		t.Fatal("read_file must return an error for .GIT/config, got nil (vulnerability: protected path was read via case variation)")
	}
	if !errors.Is(err, ErrProtected) {
		t.Fatalf("refusal of .GIT/config must be errors.Is(err, ErrProtected); got: %v", err)
	}
	if strings.Contains(out+err.Error(), secret) {
		t.Fatalf("refused read leaked .GIT/config content: out=%q err=%v", out, err)
	}
}
