package registry

import (
	"path/filepath"
	"testing"
	"time"
)

// TestLockAuthFileSerializes verifies that a second LockAuthFile blocks until
// the first holder releases.
func TestLockAuthFileSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	first, err := LockAuthFile(path)
	if err != nil {
		t.Fatalf("LockAuthFile: %v", err)
	}

	acquired := make(chan *AuthLock, 1)
	go func() {
		second, err := LockAuthFile(path)
		if err != nil {
			t.Errorf("second LockAuthFile: %v", err)
			return
		}
		acquired <- second
	}()

	select {
	case <-acquired:
		t.Fatal("second lock acquired while first was held")
	case <-time.After(200 * time.Millisecond):
		// Expected: blocked.
	}

	if err := first.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	select {
	case second := <-acquired:
		if err := second.Unlock(); err != nil {
			t.Fatalf("second Unlock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second lock not acquired after release")
	}
}

// TestWriteAuthFileLeavesNoTemp verifies the hardened atomic write leaves no
// temp files behind and the result parses back.
func TestWriteAuthFileLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	af := AuthFile{"p1": {Type: "api", Key: "k1"}}
	if err := WriteAuthFile(path, af); err != nil {
		t.Fatalf("WriteAuthFile: %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(dir, ".auth-*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	back, err := ParseAuthFile(path)
	if err != nil {
		t.Fatalf("ParseAuthFile: %v", err)
	}
	if back["p1"].Key != "k1" {
		t.Fatalf("round trip mismatch: %v", back)
	}
}
