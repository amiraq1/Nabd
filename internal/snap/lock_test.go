package snap

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	lock, err := Acquire(root, time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestAcquireContention(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// Acquire the lock in this process.
	lock, err := Acquire(root, time.Second)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer lock.Release()

	// Try to acquire again — should fail with ErrLockHeld.
	_, err = Acquire(root, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected contention error, got nil")
	}
}
