//go:build unix

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/safefs"
	"nabd/internal/snap"
)

// --- 3. writePathFromRoot: lexical path authority, same contract as readPathFromRoot.

func TestWritePathFromRootRelative(t *testing.T) {
	root := toolRoot(t)

	rel, abs, err := writePathFromRoot(root, filepath.Join("dir", "file.txt"))
	if err != nil {
		t.Fatalf("writePathFromRoot: %v", err)
	}
	if rel != filepath.Join("dir", "file.txt") {
		t.Fatalf("relative=%q, want %q", rel, filepath.Join("dir", "file.txt"))
	}
	if want := filepath.Join(root.Dir(), "dir", "file.txt"); abs != want {
		t.Fatalf("absolute=%q, want %q", abs, want)
	}
}

func TestWritePathFromRootAbsoluteInside(t *testing.T) {
	root := toolRoot(t)

	input := filepath.Join(root.Dir(), "a", "b.txt")
	rel, abs, err := writePathFromRoot(root, input)
	if err != nil {
		t.Fatalf("writePathFromRoot: %v", err)
	}
	if rel != filepath.Join("a", "b.txt") {
		t.Fatalf("relative=%q, want %q", rel, filepath.Join("a", "b.txt"))
	}
	if abs != input {
		t.Fatalf("absolute=%q, want %q", abs, input)
	}
}

func TestWritePathFromRootRejectsAbsoluteOutside(t *testing.T) {
	root := toolRoot(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")

	if _, _, err := writePathFromRoot(root, outside); err == nil {
		t.Fatal("absolute path outside root must be refused")
	}
}

func TestWritePathFromRootRejectsTraversal(t *testing.T) {
	root := toolRoot(t)

	for _, in := range []string{
		"..",
		filepath.Join("..", "escape.txt"),
		filepath.Join("src", "..", "..", "escape.txt"),
	} {
		if _, _, err := writePathFromRoot(root, in); err == nil {
			t.Errorf("writePathFromRoot(%q) must be refused", in)
		}
	}
}

// --- 4. captureFromRoot: descriptor-relative capture, authority is relative.

func TestCaptureFromRootExistingFile(t *testing.T) {
	r, dir := newReg(t)
	content := "hello\nworld\n"
	abs := filepath.Join(dir, "f.txt")
	toolWrite(t, abs, content)
	if err := os.Chmod(abs, 0o644); err != nil {
		t.Fatal(err)
	}

	rel, absPath, err := writePathFromRoot(r.root, abs)
	if err != nil {
		t.Fatal(err)
	}
	st, err := captureFromRoot(r.sh, r.root, rel, absPath)
	if err != nil {
		t.Fatalf("captureFromRoot: %v", err)
	}
	if st.Absent {
		t.Fatal("Absent=true for an existing file")
	}
	if st.Blob == "" {
		t.Error("Blob empty, want the stored content id")
	}
	if st.Size != int64(len(content)) {
		t.Errorf("Size=%d, want %d", st.Size, len(content))
	}
	if st.Mode.Perm() != 0o644 {
		t.Errorf("Mode=%v, want 0644", st.Mode)
	}
	if st.Rel != rel {
		t.Errorf("Rel=%q, want %q", st.Rel, rel)
	}
	if st.At.IsZero() {
		t.Error("At is zero, want a timestamp")
	}

	// The descriptor read must produce the same content-addressed blob as
	// CaptureBytes on the same bytes.
	want, err := r.sh.CaptureBytes(absPath, []byte(content), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if st.Blob != want.Blob {
		t.Errorf("Blob=%q, want %q (same content)", st.Blob, want.Blob)
	}
}

func TestCaptureFromRootAbsentFile(t *testing.T) {
	r, _ := newReg(t)

	rel, abs, err := writePathFromRoot(r.root, filepath.Join("nope", "missing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := captureFromRoot(r.sh, r.root, rel, abs)
	if err != nil {
		t.Fatalf("absent must not be an error: %v", err)
	}
	if !st.Absent {
		t.Error("Absent=false, want true")
	}
	if st.Blob != "" {
		t.Errorf("Blob=%q, want empty (no fake empty blob)", st.Blob)
	}
	if st.Size != 0 {
		t.Errorf("Size=%d, want 0", st.Size)
	}
	if st.Rel != rel {
		t.Errorf("Rel=%q, want %q", st.Rel, rel)
	}
}

func TestCaptureFromRootRejectsFinalSymlink(t *testing.T) {
	r, dir := newReg(t)
	toolWrite(t, filepath.Join(dir, "target.txt"), "x\n")
	toolSymlink(t, "target.txt", filepath.Join(dir, "link.txt"))

	rel, abs, err := writePathFromRoot(r.root, "link.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureFromRoot(r.sh, r.root, rel, abs); !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("err=%v, want ErrSymlink", err)
	}
}

func TestCaptureFromRootRejectsIntermediateSymlink(t *testing.T) {
	r, dir := newReg(t)
	outside := t.TempDir()
	toolWrite(t, filepath.Join(outside, "secret.txt"), "s\n")
	toolWrite(t, filepath.Join(dir, ".keep"), "")
	toolSymlink(t, outside, filepath.Join(dir, "vendor"))

	rel, abs, err := writePathFromRoot(r.root, filepath.Join("vendor", "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureFromRoot(r.sh, r.root, rel, abs); !errors.Is(err, safefs.ErrNotDirectory) {
		t.Fatalf("err=%v, want ErrNotDirectory", err)
	}
}

func TestCaptureFromRootPreservesMode(t *testing.T) {
	r, dir := newReg(t)
	abs := filepath.Join(dir, "script.sh")
	toolWrite(t, abs, "#!/bin/sh\n")
	if err := os.Chmod(abs, 0o600); err != nil {
		t.Fatal(err)
	}

	rel, absPath, err := writePathFromRoot(r.root, "script.sh")
	if err != nil {
		t.Fatal(err)
	}
	st, err := captureFromRoot(r.sh, r.root, rel, absPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode.Perm() != 0o600 {
		t.Errorf("Mode=%v, want 0600 (from the descriptor's Stat)", st.Mode)
	}
}

// --- 5. writeFromRoot: descriptor-relative write, absolute is metadata only.

func TestWriteFromRootCreatesFile(t *testing.T) {
	r, dir := newReg(t)

	rel, abs, err := writePathFromRoot(r.root, filepath.Join("sub", "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFromRoot(r.root, rel, abs, []byte("created\n"), 0o644); err != nil {
		t.Fatalf("writeFromRoot: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "sub", "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "created\n" {
		t.Errorf("content=%q, want %q", got, "created\n")
	}
}

func TestWriteFromRootReplacesFile(t *testing.T) {
	r, dir := newReg(t)
	abs := filepath.Join(dir, "f.txt")
	toolWrite(t, abs, "old\n")

	rel, absPath, err := writePathFromRoot(r.root, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFromRoot(r.root, rel, absPath, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("writeFromRoot: %v", err)
	}

	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Errorf("content=%q, want %q", got, "new\n")
	}
}

func TestWriteFromRootRejectsIntermediateSymlink(t *testing.T) {
	r, dir := newReg(t)
	outside := t.TempDir()
	toolWrite(t, filepath.Join(dir, ".keep"), "")
	toolSymlink(t, outside, filepath.Join(dir, "vendor"))

	rel, abs, err := writePathFromRoot(r.root, filepath.Join("vendor", "evil.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFromRoot(r.root, rel, abs, []byte("x\n"), 0o644); err == nil {
		t.Fatal("writing through an intermediate symlink must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.txt")); !os.IsNotExist(err) {
		t.Errorf("write escaped the root: %v", err)
	}
}

// The absolute argument is metadata. A lie in it must not redirect the write
// outside the root.
func TestWriteFromRootDoesNotUseAbsoluteMetadataAsAuthority(t *testing.T) {
	r, dir := newReg(t)
	outsideDir := t.TempDir()
	victim := filepath.Join(outsideDir, "victim.txt")
	toolWrite(t, victim, "outside-original\n")

	const rel = "inside.txt"
	fakeAbs := victim // metadata lies about the location

	if err := writeFromRoot(r.root, rel, fakeAbs, []byte("injected\n"), 0o644); err != nil {
		t.Fatalf("writeFromRoot: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "inside.txt"))
	if err != nil {
		t.Fatalf("file not written inside the root: %v", err)
	}
	if string(got) != "injected\n" {
		t.Errorf("inside content=%q, want %q", got, "injected\n")
	}

	victimGot, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(victimGot) != "outside-original\n" {
		t.Errorf("absolute metadata was used as authority: outside file=%q", victimGot)
	}
}

// --- 6. commit: secure capture + atomic write, contract preserved.

func TestCommitUsesSecureCaptureAndAtomicWrite(t *testing.T) {
	r, dir := newReg(t)
	abs := filepath.Join(dir, "sub", "f.txt")

	before, after, err := commit(context.Background(), r.root, r.sh, r.edits, r, "write_file", abs, []byte("payload\n"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !before.Absent {
		t.Error("nested new file must be absent before the write")
	}
	if after.Absent || after.Blob == "" {
		t.Errorf("after=%+v, want a present state with a blob", after)
	}

	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload\n" {
		t.Errorf("content=%q, want %q", got, "payload\n")
	}
	if n := len(r.Edits()); n != 1 {
		t.Errorf("edits=%d, want 1", n)
	}
}

func TestCommitCreatesAbsentBeforeState(t *testing.T) {
	r, dir := newReg(t)
	abs := filepath.Join(dir, "new.txt")

	before, _, err := commit(context.Background(), r.root, r.sh, r.edits, r, "write_file", abs, []byte("n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !before.Absent {
		t.Error("before.Absent=false, want true for a new file")
	}
	if before.Blob != "" {
		t.Errorf("before.Blob=%q, want empty", before.Blob)
	}

	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("LastEdit() = nil, want a record")
	}
	if rec.HashBefore != "" {
		t.Errorf("HashBefore=%q, want empty for a creation", rec.HashBefore)
	}
	if rec.BlobBefore != "" {
		t.Errorf("BlobBefore=%q, want empty for a creation", rec.BlobBefore)
	}
	if rec.HashAfter == "" || rec.BlobAfter == "" {
		t.Errorf("HashAfter=%q BlobAfter=%q, want both set", rec.HashAfter, rec.BlobAfter)
	}
}

func TestCommitPreservesExistingMode(t *testing.T) {
	r, dir := newReg(t)
	abs := filepath.Join(dir, "script.sh")
	toolWrite(t, abs, "#!/bin/sh\n")
	if err := os.Chmod(abs, 0o600); err != nil {
		t.Fatal(err)
	}

	before, after, err := commit(context.Background(), r.root, r.sh, r.edits, r, "edit_file", abs, []byte("#!/bin/sh\necho hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode.Perm() != 0o600 {
		t.Errorf("before.Mode=%v, want 0600", before.Mode)
	}
	if after.Mode.Perm() != 0o600 {
		t.Errorf("after.Mode=%v, want 0600", after.Mode)
	}

	fi, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("on-disk mode=%v, want 0600", fi.Mode().Perm())
	}

	if rec := r.LastEdit(); rec == nil || rec.ModeBefore.Perm() != 0o600 {
		t.Errorf("ModeBefore not preserved: %+v", rec)
	}
}

func TestCommitVerifiesThroughSecureCapture(t *testing.T) {
	r, dir := newReg(t)
	abs := filepath.Join(dir, "v.txt")

	_, after, err := commit(context.Background(), r.root, r.sh, r.edits, r, "write_file", abs, []byte("verify me\n"))
	if err != nil {
		t.Fatal(err)
	}

	rel, absPath, err := writePathFromRoot(r.root, abs)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := captureFromRoot(r.sh, r.root, rel, absPath)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Unchanged(after, actual) {
		t.Errorf("after=%+v does not match a fresh secure capture %+v", after, actual)
	}
}

func TestCommitRejectsSymlinkWithoutCapturingOutsideContent(t *testing.T) {
	r, dir := newReg(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	const secretContent = "TOP-SECRET-CONTENT\n"
	toolWrite(t, secret, secretContent)

	link := filepath.Join(dir, "leak.txt")
	toolSymlink(t, secret, link)

	if _, _, err := commit(context.Background(), r.root, r.sh, r.edits, r, "write_file", link, []byte("attacker\n")); err == nil {
		t.Fatal("commit through a final symlink must be refused")
	}

	got, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != secretContent {
		t.Errorf("outside content changed: %q", got)
	}

	if blobsContain(t, r.sh.StoreDir(), secretContent) {
		t.Error("outside content leaked into the shadow store")
	}
}

// blobsContain reports whether any regular file beneath store holds needle.
func blobsContain(t *testing.T, store, needle string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(store, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		if strings.Contains(string(b), needle) {
			found = true
		}
		return nil
	})
	return found
}
