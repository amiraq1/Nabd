//go:build unix

package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveFileRemovesRegularFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "dir", "gone.txt")
	mustWrite(t, p, "x")

	if err := RemoveFile(root, filepath.Join("dir", "gone.txt")); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file still present: %v", err)
	}
}

func TestRemoveFileMissingIsNotExist(t *testing.T) {
	root := t.TempDir()
	if err := RemoveFile(root, "nope.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing err=%v, want os.ErrNotExist", err)
	}
}

func TestRemoveFileDoesNotCreateParents(t *testing.T) {
	root := t.TempDir()
	if err := RemoveFile(root, filepath.Join("missing", "f.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing parent err=%v, want os.ErrNotExist", err)
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatal("RemoveFile created a parent directory")
	}
}

func TestRemoveFileRemovesFinalSymlinkNotReferent(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret")
	mustWrite(t, filepath.Join(root, ".keep"), "")
	target := filepath.Join(outside, "secret.txt")
	mustSymlink(t, target, filepath.Join(root, "link.txt"))

	if err := RemoveFile(root, "link.txt"); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "link.txt")); !os.IsNotExist(err) {
		t.Fatal("symlink not removed")
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "secret" {
		t.Fatalf("referent touched: content=%q err=%v", b, err)
	}
}

func TestRemoveFileRefusesIntermediateSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret")
	mustWrite(t, filepath.Join(root, ".keep"), "")
	mustSymlink(t, outside, filepath.Join(root, "vendor"))

	if err := RemoveFile(root, filepath.Join("vendor", "secret.txt")); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("intermediate symlink err=%v, want ErrNotDirectory", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatal("outside file removed through symlink")
	}
}

func TestRemoveFileRefusesDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFile(root, "dir"); err == nil {
		t.Fatal("RemoveFile must refuse a directory target")
	}
}

func TestRemoveFileRejectsInvalidPaths(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrEmptyPath},
		{"dot", ".", ErrInvalidTarget},
		{"trailing", "dir" + string(filepath.Separator), ErrInvalidTarget},
		{"absolute", string(filepath.Separator) + "etc/passwd", ErrAbsolutePath},
		{"traversal", ".." + string(filepath.Separator) + "outside", ErrTraversal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RemoveFile(root, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("RemoveFile(%q) err=%v, want %v", tc.in, err, tc.want)
			}
		})
	}
}
