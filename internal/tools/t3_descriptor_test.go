//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
)

// The invariant T3 must not break: undo rebuilds the "current" state through
// captureFromRoot. Its Blob carries the s256: prefix, so the HashAfter
// comparison stays meaningful. If that ever changed, the check would go
// permanently negative and /undo would silently overwrite the user's work.
func TestCaptureFromRootBlobMatchesHashAfter(t *testing.T) {
	r, dir := newReg(t)
	const content = "hello\n"
	abs := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rel, absPath, err := writePathFromRoot(r.root, "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	st, err := captureFromRoot(r.sh, r.root, rel, absPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.Blob, "s256:") {
		t.Fatalf("Blob=%q, want the s256: prefix the journal hashes assume", st.Blob)
	}
	if st.Blob[5:] != sha256hex([]byte(content)) {
		t.Fatalf("Blob digest=%q, want %q", st.Blob[5:], sha256hex([]byte(content)))
	}
}

// The user edits a file after the agent wrote it: /undo must refuse, and the
// user's content must survive.
func TestUndoRefusesModifiedAfterAgentWrite(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()
	path := filepath.Join(dir, "work.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(map[string]any{"path": "work.txt", "content": "agent wrote this\n"})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file: ok=%v err=%v", ok, err)
	}
	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("LastEdit() = nil")
	}

	// The user edits the file after the agent's write.
	if err := os.WriteFile(path, []byte("user changed it\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := r.PersistedUndo([]*agent.EditRecord{rec}, 1)
	if len(res) != 1 || res[0].OK || res[0].Note != ErrUndoConflictChanged.Error() {
		t.Fatalf("expected refusal with ErrUndoConflictChanged, got %+v", res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "user changed it\n" {
		t.Fatalf("user content overwritten: %q", got)
	}
}

// readSourceFromRoot must read the relative path; a lying absolute path is
// metadata only.
func TestReadSourceFromRootUsesRelativeAuthority(t *testing.T) {
	r, dir := newReg(t)
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "out.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := readSourceFromRoot(r.root, "in.txt", outside, 1<<20)
	if err != nil {
		t.Fatalf("readSourceFromRoot: %v", err)
	}
	if string(data) != "inside\n" {
		t.Fatalf("content=%q, want the file inside root", data)
	}
}

func TestReadSourceFromRootEnforcesLimit(t *testing.T) {
	r, dir := newReg(t)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("a", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, abs, err := writePathFromRoot(r.root, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readSourceFromRoot(r.root, rel, abs, 10); err == nil {
		t.Fatal("readSourceFromRoot must refuse a file over the limit")
	}
}

// removeFromRoot must delete the relative path; a lying absolute path must not
// redirect the deletion outside root.
func TestRemoveFromRootUsesRelativeAuthority(t *testing.T) {
	r, dir := newReg(t)
	inside := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "keep.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeFromRoot(r.root, "victim.txt", outside); err != nil {
		t.Fatalf("removeFromRoot: %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatal("inside file was not removed")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("outside file removed through lying absolute metadata")
	}
}
