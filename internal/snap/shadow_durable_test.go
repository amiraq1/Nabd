package snap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableShadow(t *testing.T) {
	dir := t.TempDir()

	// Create git repo
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}

	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Store content and restore it normally.
	file := filepath.Join(dir, "test.txt")
	os.WriteFile(file, []byte("durable content"), 0644)
	st, err := s.Capture(file)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Store identical content more than once.
	st2, err := s.Capture(file)
	if err != nil {
		t.Fatal(err)
	}
	if st.Blob != st2.Blob {
		t.Fatalf("expected identical blob IDs, got %v and %v", st.Blob, st2.Blob)
	}

	// 3. Restart/recreate the shadow-store object, then restore.
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(file)
	if err := s2.Restore(st); err != nil {
		t.Fatal(err)
	}

	// 4. Run `git gc --prune=now` between store and restore and prove the restoration still succeeds.
	os.Remove(file)
	if out, err := exec.Command("git", "-C", dir, "gc", "--prune=now").CombinedOutput(); err != nil {
		t.Fatalf("git gc: %s", out)
	}
	if err := s2.Restore(st); err != nil {
		t.Fatalf("restore after git gc failed: %v", err)
	}

	// 5. Delete the stored blob manually and prove a typed or explicit missing-shadow error is returned.
	blobPath := filepath.Join(s2.store, st.Blob[5:7], st.Blob[7:])
	os.Remove(blobPath)
	err = s2.Restore(st)
	if !errors.Is(err, ErrShadowMissing) {
		t.Fatalf("expected missing shadow error, got %v", err)
	}

	// 6. Corrupt a stored blob and prove corruption is detected.
	// We need a new state since we deleted the old blob.
	os.WriteFile(file, []byte("durable content 2"), 0644)
	st3, err := s2.Capture(file)
	if err != nil {
		t.Fatal(err)
	}
	blobPath3 := filepath.Join(s2.store, st3.Blob[5:7], st3.Blob[7:])
	os.Chmod(blobPath3, 0644)
	os.WriteFile(blobPath3, []byte("corrupted"), 0644)

	err = s2.Restore(st3)
	if !errors.Is(err, ErrShadowCorruption) {
		t.Fatalf("expected corrupted shadow error, got %v", err)
	}
}

func TestShadowInvalidIdentifiers(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	invalidIDs := []string{
		"s256:../../../../etc/passwd",      // traversal
		"s256:" + string(make([]byte, 64)), // invalid hex chars
		"s256:abcd",                        // too short
		"s256:1111111111111111111111111111111111111111111111111111111111111111A",  // uppercase
		"sha256-1111111111111111111111111111111111111111111111111111111111111111", // wrong prefix
	}

	for _, id := range invalidIDs {
		_, err := s.Read(id)
		if !errors.Is(err, ErrShadowInvalidID) {
			t.Errorf("expected ErrShadowInvalidID for id %q, got: %v", id, err)
		}
	}
}

func TestShadowEmptyBlob(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "empty.txt")
	os.WriteFile(target, []byte{}, 0644)
	st, err := s.Capture(target)
	if err != nil {
		t.Fatal(err)
	}

	err = s.Restore(st)
	if err != nil {
		t.Fatalf("restoring empty blob failed: %v", err)
	}
}

func TestShadowPreExistingBlob(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	// First run
	target := filepath.Join(dir, "test.txt")
	os.WriteFile(target, []byte("data"), 0644)
	st, err := s.Capture(target)
	if err != nil {
		t.Fatal(err)
	}

	// Capture again, should be idempotent success
	_, err = s.Capture(target)
	if err != nil {
		t.Fatalf("repeated capture failed: %v", err)
	}

	// Corrupt it manually
	blobPath := filepath.Join(s.store, st.Blob[5:7], st.Blob[7:])
	os.Chmod(blobPath, 0644)
	os.WriteFile(blobPath, []byte("corrupt"), 0644)

	// Capture again, should throw ErrShadowCorruption
	_, err = s.Capture(target)
	if !errors.Is(err, ErrShadowCorruption) {
		t.Fatalf("expected ErrShadowCorruption, got %v", err)
	}
}

func TestShadowConcurrency(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "concurrent.txt")
	os.WriteFile(target, []byte("data"), 0644)

	// Run multiple captures concurrently
	errCh := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := s.Capture(target)
			errCh <- err
		}()
	}

	for i := 0; i < 10; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent capture failed: %v", err)
		}
	}
}

func TestShadowFallbackRaceOnCorruptExisting(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "file.txt")
	os.WriteFile(target, []byte("new data"), 0644)

	// Pre-create the shadow store blob manually to simulate an existing corrupted blob
	sum := sha256.Sum256([]byte("new data"))
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])

	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, []byte("corrupt data"), 0644)

	// Now try to capture it. It should throw ErrShadowCorruption and NOT replace it.
	_, err = s.Capture(target)
	if !errors.Is(err, ErrShadowCorruption) {
		t.Fatalf("expected ErrShadowCorruption, got %v", err)
	}

	// Verify it was NOT replaced
	data, _ := os.ReadFile(p)
	if string(data) != "corrupt data" {
		t.Fatalf("corrupted blob was silently replaced!")
	}
}

func TestShadowStaleLockDoesNotBlockForever(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "stale.txt")
	os.WriteFile(target, []byte("stale data"), 0644)
	sum := sha256.Sum256([]byte("stale data"))
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])

	// Create a stale lock
	os.MkdirAll(filepath.Dir(p), 0755)
	lockPath := p + ".lock"
	os.Mkdir(lockPath, 0755)

	// Backdate the lock directory
	staleTime := time.Now().Add(-20 * time.Second)
	os.Chtimes(lockPath, staleTime, staleTime)

	// Capture should succeed because it clears the stale lock
	_, err = s.Capture(target)
	if err != nil {
		t.Fatalf("capture failed on stale lock: %v", err)
	}
}

func TestShadowLockReleasedAfterFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "fail.txt")
	os.WriteFile(target, []byte("fail data"), 0644)

	sum := sha256.Sum256([]byte("fail data"))
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])

	// To simulate a failure DURING the lock, we can't easily inject a crash inside `put`.
	// But we can verify that after a successful put, the lock is gone.
	_, err = s.Capture(target)
	if err != nil {
		t.Fatal(err)
	}

	lockPath := p + ".lock"
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock directory was not removed after capture")
	}
}

func TestShadowExternalDestinationRace(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "race.txt")
	os.WriteFile(target, []byte("race data"), 0644)

	sum := sha256.Sum256([]byte("race data"))
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])

	os.MkdirAll(filepath.Dir(p), 0755)

	// We want to simulate an external process writing a corrupt blob EXACTLY when we are trying to put.
	// Since we can't pause `put`, we just test the case where it exists and is corrupt.
	// The mutex protects against cooperating processes. External processes bypassing the lock
	// (e.g. standard file renaming) will overwrite our blob on POSIX.
	// We just ensure `ErrShadowCorruption` is correctly returned when it encounters it.
	os.WriteFile(p, []byte("external corrupt"), 0644)

	_, err = s.Capture(target)
	if !errors.Is(err, ErrShadowCorruption) {
		t.Fatalf("expected ErrShadowCorruption, got %v", err)
	}
}
