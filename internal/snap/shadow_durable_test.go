package snap

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
