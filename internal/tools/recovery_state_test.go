package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/snap"
)

func recoveryHash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func TestReconcileMutationClassifiesWorkingTreeWithoutWriting(t *testing.T) {
	rootDir := t.TempDir()
	root, err := NewRoot(rootDir)
	if err != nil {
		t.Fatalf("NewRoot: %v", err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatalf("snap.New: %v", err)
	}
	path := filepath.Join(rootDir, "notes.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	rec := &agent.EditRecord{
		MutationID: "m1",
		Path:       "notes.txt",
		HashBefore: recoveryHash("before"),
		HashAfter:  recoveryHash("after"),
	}
	reg := NewRegistry(root, sh)

	assertState := func(want MutationRecoveryState) {
		t.Helper()
		got, err := reg.ReconcileMutation(rec)
		if err != nil {
			t.Fatalf("ReconcileMutation: %v", err)
		}
		if got != want {
			t.Fatalf("state=%q, want %q", got, want)
		}
	}

	assertState(MutationNotPublished)
	if got, _ := os.ReadFile(path); string(got) != "before" {
		t.Fatalf("reconciliation changed the project file: %q", got)
	}
	if err := os.WriteFile(path, []byte("after"), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	assertState(MutationPublished)
	if err := os.WriteFile(path, []byte("human change"), 0o600); err != nil {
		t.Fatalf("write conflict: %v", err)
	}
	assertState(MutationConflict)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	assertState(MutationMissing)
}

func TestReconcileMutationRejectsUnsafePath(t *testing.T) {
	root, err := NewRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NewRoot: %v", err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatalf("snap.New: %v", err)
	}
	reg := NewRegistry(root, sh)
	if _, err := reg.ReconcileMutation(&agent.EditRecord{Path: "../outside.txt"}); err == nil {
		t.Fatal("unsafe mutation path was accepted")
	}
}
