package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMkdirParentDirsInheritsPrivateAncestorMode(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(private, "one", "two", "file.txt")
	if err := mkdirParentDirs(target); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(private, "one"), filepath.Join(private, "one", "two")} {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o700 {
			t.Fatalf("%s mode = %04o, want 0700", dir, got)
		}
	}
}
