//go:build android

package safefs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
}

func TestOpenReadReadsRegularFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "dir", "file.txt"), "hello from fd")

	f, err := OpenRead(root, filepath.Join("dir", "file.txt"))
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello from fd" {
		t.Fatalf("content=%q", got)
	}
}

func TestOpenReadResolvesSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	mustWrite(t, filepath.Join(real, "f.txt"), "rooted")
	alias := filepath.Join(base, "alias")
	mustSymlink(t, real, alias)

	f, err := OpenRead(alias, "f.txt")
	if err != nil {
		t.Fatalf("OpenRead via symlinked root: %v", err)
	}
	defer f.Close()

	got, _ := io.ReadAll(f)
	if string(got) != "rooted" {
		t.Fatalf("content=%q", got)
	}
}

func TestOpenReadRefusesFinalSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret")
	mustWrite(t, filepath.Join(root, ".keep"), "")
	mustSymlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(root, "notes.txt"))

	if _, err := OpenRead(root, "notes.txt"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("final symlink error=%v, want ErrSymlink", err)
	}
}

func TestOpenReadRefusesIntermediateSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret")
	mustWrite(t, filepath.Join(root, ".keep"), "")
	mustSymlink(t, outside, filepath.Join(root, "vendor"))

	// With O_NOFOLLOW|O_DIRECTORY, Android reports ENOTDIR for a symlink
	// component rather than ELOOP, so a symlink intermediate is classified
	// ErrNotDirectory. The requirement is refusal, not a precise type.
	if _, err := OpenRead(root, filepath.Join("vendor", "secret.txt")); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("intermediate symlink error=%v, want ErrNotDirectory", err)
	}
}

func TestOpenReadRefusesIntermediateRegularFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "regular")

	if _, err := OpenRead(root, filepath.Join("a.txt", "b.txt")); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("intermediate regular file error=%v, want ErrNotDirectory", err)
	}
}

func TestOpenReadRefusesFIFO(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	// O_NONBLOCK is required: a blocking open of a FIFO with no writer would
	// hang. Reaching ErrNotRegular proves the descriptor was obtained without
	// blocking and then rejected by Fstat.
	if _, err := OpenRead(root, "pipe"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("FIFO error=%v, want ErrNotRegular", err)
	}
}

func TestOpenReadRefusesDirectory(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "dir", "f.txt"), "x")

	if _, err := OpenRead(root, "dir"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("directory error=%v, want ErrNotRegular", err)
	}
	if _, err := OpenRead(root, "."); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("dot error=%v, want ErrNotRegular", err)
	}
}

func TestOpenReadMissingFileIsNotExist(t *testing.T) {
	root := t.TempDir()

	if _, err := OpenRead(root, "nope.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing error=%v, want ErrNotExist", err)
	}
}

func TestOpenReadRejectsInvalidPaths(t *testing.T) {
	root := t.TempDir()

	cases := []struct {
		name  string
		input string
		want  error
	}{
		{"empty", "", ErrEmptyPath},
		{"nul", "safe" + string(rune(0)) + "escape", ErrNULPath},
		{"absolute", string(filepath.Separator) + "etc/passwd", ErrAbsolutePath},
		{"traversal", ".." + string(filepath.Separator) + "outside", ErrTraversal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := OpenRead(root, tc.input); !errors.Is(err, tc.want) {
				t.Fatalf("OpenRead(%q) error=%v, want %v", tc.input, err, tc.want)
			}
		})
	}
}
