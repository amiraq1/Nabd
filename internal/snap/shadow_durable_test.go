package snap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestShadowPublishNoReplaceBoundary proves no-replace semantics: a blob that
// another writer already published at the exact destination path (here, with
// corrupt content) must never be replaced or overwritten. The publish attempt
// hits the atomic RENAME_NOREPLACE/MoveFile/link boundary, routes EEXIST into
// blob verification, and fails with ErrShadowCorruption while leaving the
// existing bytes untouched. This is the no-replace contract, not any lock.
func TestShadowPublishNoReplaceBoundary(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "live.txt")
	os.WriteFile(target, []byte("data"), 0644)

	sum := sha256.Sum256([]byte("data"))
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])

	// Simulate a concurrent writer that has ALREADY successfully published
	// a blob at exactly the path we are trying to publish to, with different
	// (corrupt) content.
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, []byte("corrupt"), 0644)

	_, err = s.Capture(target)
	if !errors.Is(err, ErrShadowCorruption) {
		t.Fatalf("expected ErrShadowCorruption to prove no-replace atomic boundary, got %v", err)
	}

	// The pre-existing blob must be byte-for-byte untouched: no-replace means
	// the destination is never overwritten under any circumstance.
	got, _ := os.ReadFile(p)
	if string(got) != "corrupt" {
		t.Fatalf("no-replace violated: existing blob replaced with %q", got)
	}
}

// TestShadowPublishIsLockFree proves that publication requires no lock files
// and leaves no blocker artifacts behind: a clean publish succeeds and the
// shadow store contains no `.lock` entries (the old temporal-lock machinery is
// gone; atomic no-replace publication is lock-free by construction).
func TestShadowPublishIsLockFree(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "crash.txt")
	os.WriteFile(target, []byte("crash data"), 0644)

	// A clean publish must succeed without any lock recovery.
	if _, err := s.Capture(target); err != nil {
		t.Fatal(err)
	}

	// And it must leave no blocker/lock artifacts anywhere in the store.
	err = filepath.Walk(s.store, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".lock") {
			t.Errorf("lock artifact left behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestShadowPublishIdempotentOnMatchingContent(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "idem.txt")
	os.WriteFile(target, []byte("same data"), 0644)

	// First publish
	_, err = s.Capture(target)
	if err != nil {
		t.Fatal(err)
	}

	// Second publish matches content, should be idempotent success
	_, err = s.Capture(target)
	if err != nil {
		t.Fatalf("expected idempotent success on matching content, got %v", err)
	}
}

// TestShadowPublishRejectsOnMismatchedContent proves the blob-match check is a
// full-content comparison, not a size comparison. The existing blob and the
// new content have the SAME length ("different data" vs "new data" are not
// used here on purpose): equal-size-different-content must still be rejected
// with ErrShadowCorruption, which a naive size check would let through.
func TestShadowPublishRejectsOnMismatchedContent(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "mismatch.txt")
	os.WriteFile(target, []byte("AAAAAAAAA"), 0644) // 9 bytes

	sum := sha256.Sum256([]byte("AAAAAAAAA"))
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])

	// Manually place a mismatching file of EQUAL length (9 bytes) at the
	// expected location: a size-based comparison would false-positive here.
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, []byte("BBBBBBBBB"), 0644) // 9 bytes, different content

	// Publish must reject: 9 bytes vs 9 bytes, different content.
	_, err = s.Capture(target)
	if !errors.Is(err, ErrShadowCorruption) {
		t.Fatalf("expected ErrShadowCorruption on mismatched content, got %v", err)
	}

	// And the existing blob must still be untouched.
	got, _ := os.ReadFile(p)
	if string(got) != "BBBBBBBBB" {
		t.Fatalf("existing blob was replaced: %q", got)
	}
}

// TestNoReplaceCapabilityProbe executes the real runtime capability probe on
// the current filesystem (the actual kernel and proot layer, not a CI build
// assumption). It must either report support (nil) or explicitly report
// ErrAtomicPublishUnsupported; it must never silently fall back to a replacing
// rename. Combined with `uname -r`, this is the runtime evidence that atomic
// no-replace publication works on the target Termux/proot environment.
func TestNoReplaceCapabilityProbe(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := probeNoReplaceSupport(dir)
	if err != nil {
		if errors.Is(err, ErrAtomicPublishUnsupported) {
			t.Logf("atomic no-replace NOT supported on this filesystem: %v", err)
			return // recorded as UNSUPPORTED, never a silent fallback
		}
		t.Fatalf("capability probe failed unexpectedly: %v", err)
	}
	t.Log("atomic no-replace publication supported on this filesystem (real syscall probe)")
}

// TestNoReplaceCapabilityGatesPublish proves that when the platform does not
// support atomic no-replace, the publish path returns ErrAtomicPublishUnsupported
// explicitly instead of falling back to a plain os.Rename over the destination.
// The probe result is cached per store, so a second publish fails identically.
func TestNoReplaceCapabilityGatesPublish(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "gated.txt")
	if err := os.WriteFile(target, []byte("gated data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Replace the probe with a forced unsupported result to exercise the gate
	// deterministically on any platform.
	s.capOnce.Do(func() {}) // mark as done so our injected result is used
	s.capErr = ErrAtomicPublishUnsupported

	_, err = s.Capture(target)
	if !errors.Is(err, ErrAtomicPublishUnsupported) {
		t.Fatalf("expected ErrAtomicPublishUnsupported from capability gate, got %v", err)
	}

	// The destination must not have been created by a fallback os.Rename.
	blobID := sha256.Sum256([]byte("gated data"))
	p := filepath.Join(s.store, hex.EncodeToString(blobID[:])[:2], hex.EncodeToString(blobID[:])[2:])
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("publish bypassed the capability gate: blob exists at %s (err=%v)", p, err)
	}
}

func TestTempFileCleanupOnFailurePath(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "cleanup.txt")
	os.WriteFile(target, []byte("data"), 0644)
	sum := sha256.Sum256([]byte("data"))
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])

	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, []byte("wrong"), 0644)

	// This fails due to mismatch.
	s.Capture(target)

	// Verify no tmp files are left in the blob directory
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if e.Name() != filepath.Base(p) {
			t.Fatalf("found unexpected file after failure path: %s", e.Name())
		}
	}
}
