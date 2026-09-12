//go:build unix

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/safefs"
)

func toolRoot(t *testing.T) *Root {
	t.Helper()
	r, err := NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func toolWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func toolSymlink(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
}

// 1 + 2: relative input returns a descriptor and the joined absolute path.
func TestReadPathFromRootRelative(t *testing.T) {
	root := toolRoot(t)

	rel, abs, err := readPathFromRoot(root, filepath.Join("dir", "file.txt"))
	if err != nil {
		t.Fatalf("readPathFromRoot: %v", err)
	}
	if rel != filepath.Join("dir", "file.txt") {
		t.Fatalf("relative=%q", rel)
	}
	want := filepath.Join(root.Dir(), "dir", "file.txt")
	if abs != want {
		t.Fatalf("absolute=%q, want %q", abs, want)
	}
}

// 3: an absolute path inside root is converted to a relative one.
func TestReadPathFromRootAbsoluteInside(t *testing.T) {
	root := toolRoot(t)

	input := filepath.Join(root.Dir(), "a", "b.txt")
	rel, abs, err := readPathFromRoot(root, input)
	if err != nil {
		t.Fatalf("readPathFromRoot: %v", err)
	}
	if rel != filepath.Join("a", "b.txt") {
		t.Fatalf("relative=%q", rel)
	}
	if abs != input {
		t.Fatalf("absolute=%q, want %q", abs, input)
	}
}

// 4: an absolute path outside root yields ".." and is refused by Normalize.
func TestReadPathFromRootAbsoluteOutside(t *testing.T) {
	root := toolRoot(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")

	if _, _, err := readPathFromRoot(root, outside); err == nil {
		t.Fatal("absolute path outside root must be refused")
	}
}

func TestOpenReadFromRootRelative(t *testing.T) {
	root := toolRoot(t)
	toolWrite(t, filepath.Join(root.Dir(), "f.txt"), "hello")

	f, abs, err := openReadFromRoot(root, "f.txt")
	if err != nil {
		t.Fatalf("openReadFromRoot: %v", err)
	}
	defer f.Close()

	if want := filepath.Join(root.Dir(), "f.txt"); abs != want {
		t.Fatalf("abs=%q, want %q", abs, want)
	}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("content=%q", got)
	}
}

// 5: final symlink refused.
func TestOpenReadFromRootRefusesFinalSymlink(t *testing.T) {
	root := toolRoot(t)
	toolWrite(t, filepath.Join(root.Dir(), "target.txt"), "x")
	toolSymlink(t, "target.txt", filepath.Join(root.Dir(), "link.txt"))

	if _, _, err := openReadFromRoot(root, "link.txt"); !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("final symlink err=%v, want ErrSymlink", err)
	}
}

// 6: intermediate symlink refused.
func TestOpenReadFromRootRefusesIntermediateSymlink(t *testing.T) {
	root := toolRoot(t)
	outside := t.TempDir()
	toolWrite(t, filepath.Join(outside, "secret.txt"), "s")
	toolWrite(t, filepath.Join(root.Dir(), ".keep"), "")
	toolSymlink(t, outside, filepath.Join(root.Dir(), "vendor"))

	if _, _, err := openReadFromRoot(root, filepath.Join("vendor", "secret.txt")); !errors.Is(err, safefs.ErrNotDirectory) {
		t.Fatalf("intermediate symlink err=%v, want ErrNotDirectory", err)
	}
}

// 7: an internal symlink is refused too (all symlinks under root are refused).
func TestOpenReadFromRootRefusesInternalSymlink(t *testing.T) {
	root := toolRoot(t)
	toolWrite(t, filepath.Join(root.Dir(), "target.txt"), "x")
	toolSymlink(t, "target.txt", filepath.Join(root.Dir(), "inside.txt"))

	if _, _, err := openReadFromRoot(root, "inside.txt"); !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("internal symlink err=%v, want ErrSymlink", err)
	}
}

// 8: a missing file stays os.ErrNotExist.
func TestOpenReadFromRootMissingKeepsNotExist(t *testing.T) {
	root := toolRoot(t)

	if _, _, err := openReadFromRoot(root, "nope.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing err=%v, want ErrNotExist", err)
	}
}

// 3 (end to end): reading through an absolute inside-root path keeps
// ReadCredit.Path equal to that absolute path.
func TestReadFileCreditPathForAbsoluteInside(t *testing.T) {
	r, dir := newReg(t)
	target := filepath.Join(dir, "target.txt")
	toolWrite(t, target, "one\ntwo\n")

	raw, _ := json.Marshal(map[string]any{"path": target})
	out, err := r.RunDetailed(context.Background(), "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}
	if out.ReadCredit.Path != target {
		t.Fatalf("ReadCredit.Path=%q, want %q", out.ReadCredit.Path, target)
	}
}

// 10: the reported text, hash, and line count all come from one read of the
// descriptor.
func TestReadFileDescriptorIsTheSourceOfTruth(t *testing.T) {
	r, dir := newReg(t)
	content := "alpha\nbeta\ngamma\n"
	toolWrite(t, filepath.Join(dir, "src.txt"), content)

	raw, _ := json.Marshal(map[string]any{"path": "src.txt"})
	out, err := r.RunDetailed(context.Background(), "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}

	sum := sha256.Sum256([]byte(content))
	if want := hex.EncodeToString(sum[:]); out.ReadCredit.Hash != want {
		t.Fatalf("ReadCredit.Hash=%q, want %q", out.ReadCredit.Hash, want)
	}
	if out.LinesRead != 3 {
		t.Fatalf("LinesRead=%d, want 3", out.LinesRead)
	}
	if !strings.Contains(out.Text, "alpha") || !strings.Contains(out.Text, "gamma") {
		t.Fatalf("text=%q", out.Text)
	}
}
