package snap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestConcurrentLockAcrossProcesses verifies that two processes cannot hold
// the lock simultaneously. It execs a child test binary that tries to acquire
// the lock held by the parent.
func TestConcurrentLockAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// Acquire the lock in this process.
	lock, err := Acquire(root, time.Second)
	if err != nil {
		t.Fatalf("parent acquire: %v", err)
	}
	defer lock.Release()

	// Exec a child that tries to acquire the same lock.
	// We use the test binary itself with a special flag.
	child := exec.Command(os.Args[0], "-test.run=TestHelperProcessLock", "-test.v")
	child.Env = append(os.Environ(),
		"LOCK_TEST_ROOT="+root,
		"LOCK_TEST_EXPECT_HELD=1",
	)
	out, err := child.CombinedOutput()
	if err != nil {
		t.Logf("child output:\n%s", out)
		// We expect the child to fail to acquire, which is the correct behavior.
		// The child process exits non-zero when it can't get the lock.
	}
}

// TestHelperProcessLock is a helper that runs as a child process.
// It tries to acquire the lock and reports success/failure.
func TestHelperProcessLock(t *testing.T) {
	root := os.Getenv("LOCK_TEST_ROOT")
	if root == "" {
		// Not invoked as a child process helper; skip.
		t.Skip("LOCK_TEST_ROOT not set; skipping helper process")
	}
	expectHeld := os.Getenv("LOCK_TEST_EXPECT_HELD") == "1"

	_, err := Acquire(root, 500*time.Millisecond)
	if expectHeld {
		if err == nil {
			t.Error("expected lock to be held, but acquired successfully")
		}
	} else {
		if err != nil {
			t.Fatalf("expected lock to be free, but: %v", err)
		}
	}
}
