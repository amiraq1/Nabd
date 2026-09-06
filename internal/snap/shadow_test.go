package snap

import (
	"os"
	"path/filepath"
	"testing"
)

func mk(t *testing.T) *Shadow {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoundTrip(t *testing.T) {
	s := mk(t)
	p := filepath.Join(s.root, "src", "main.go")

	// Absent -> created -> restored to absent.
	before, err := s.Capture(p)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Absent {
		t.Fatal("a missing file must record as absent")
	}
	if err := WriteAtomic(p, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(before); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("undo of a creation must delete the file")
	}

	// Existing -> modified -> restored to the old bytes.
	orig := []byte("original\n")
	if err := WriteAtomic(p, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := s.Capture(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Absent || st.Blob == "" || st.Size != int64(len(orig)) {
		t.Fatalf("bad state: %+v", st)
	}
	if err := WriteAtomic(p, []byte("ruined\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(st); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != string(orig) {
		t.Errorf("restored %q, want %q", got, orig)
	}
}

// Identical content must hash identically, and different content must
// not -- the only property Unchanged actually relies on.
func TestUnchanged(t *testing.T) {
	s := mk(t)
	a := filepath.Join(s.root, "a.txt")
	b := filepath.Join(s.root, "b.txt")
	if err := WriteAtomic(a, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(b, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sa, _ := s.Capture(a)
	sb, _ := s.Capture(b)
	if !Unchanged(sa, sb) {
		t.Error("identical content must compare equal")
	}

	after, err := s.CaptureBytes(a, []byte("different\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if Unchanged(sa, after) {
		t.Error("different content must not compare equal")
	}

	absent := State{Rel: "gone", Absent: true}
	if Unchanged(sa, absent) || !Unchanged(absent, State{Absent: true}) {
		t.Error("absence must only equal absence")
	}
	if Unchanged(State{}, State{}) {
		t.Error("two empty states must not compare equal")
	}
}

func TestCaptureRefusesDirsAndLinks(t *testing.T) {
	s := mk(t)
	if err := os.Mkdir(filepath.Join(s.root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(filepath.Join(s.root, "d")); err == nil {
		t.Error("a directory must not be captured")
	}
	target := filepath.Join(s.root, "real.txt")
	if err := WriteAtomic(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := filepath.Join(s.root, "link.txt")
	if err := os.Symlink(target, l); err != nil {
		t.Skip("no symlinks")
	}
	if _, err := s.Capture(l); err == nil {
		t.Error("a symlink must not be captured")
	}
}

func TestDiscardRemovesOrphanBlob(t *testing.T) {
	s := mk(t)
	abs := filepath.Join(s.root, "f.txt")

	// CaptureBytes writes a blob to the shadow store.
	st, err := s.CaptureBytes(abs, []byte("temporary content\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if st.Blob == "" {
		t.Fatal("expected a blob id from CaptureBytes")
	}
	// Confirm the blob exists on disk.
	blobPath := filepath.Join(s.store, st.Blob[5:7], st.Blob[7:])
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("blob not written: %v", err)
	}

	// Discard must remove it.
	if err := s.Discard(st.Blob); err != nil {
		t.Fatalf("Discard failed: %v", err)
	}
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Error("blob still exists after Discard")
	}

	// Discard of an already-removed id is idempotent (no error).
	if err := s.Discard(st.Blob); err != nil {
		t.Fatalf("second Discard should be idempotent: %v", err)
	}
	// Discard of an empty id is a no-op.
	if err := s.Discard(""); err != nil {
		t.Fatalf("Discard(\"\") should be no-op: %v", err)
	}
}

func TestWriteAtomicLeavesNoDebris(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.txt")
	if err := WriteAtomic(p, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Errorf("%d entries left in the directory, want 1", len(ents))
	}
}
