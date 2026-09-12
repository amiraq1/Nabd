//go:build android

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With mkdirParentDirs no longer called on Android, parent-directory creation
// is owned by safefs through the dirfd walk. A nested write must still create
// every missing directory, and no temporary file may survive the write.
func TestT2dNestedWriteCreatesParentsAndLeavesNoTemps(t *testing.T) {
	r, dir := newReg(t)
	abs := filepath.Join(dir, "a", "b", "c", "deep.txt")

	if _, _, err := commit(context.Background(), r.root, r.sh, r.edits, r, "write_file", abs, []byte("deep\n")); err != nil {
		t.Fatalf("commit nested: %v", err)
	}

	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("nested file not created: %v", err)
	}
	if string(got) != "deep\n" {
		t.Errorf("content=%q, want %q", got, "deep\n")
	}
	for _, d := range []string{"a", "a/b", "a/b/c"} {
		fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(d)))
		if err != nil || !fi.IsDir() {
			t.Errorf("missing created directory %q: %v", d, err)
		}
	}

	if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
		t.Errorf("temp files survived a successful write: %v", leftovers)
	}
}

// A refused write (intermediate symlink) must leave no temporary file behind.
func TestT2dFailedWriteLeavesNoTemps(t *testing.T) {
	r, dir := newReg(t)
	outside := t.TempDir()
	toolWrite(t, filepath.Join(dir, ".keep"), "")
	toolSymlink(t, outside, filepath.Join(dir, "vendor"))

	abs := filepath.Join(dir, "vendor", "x.txt")
	if _, _, err := commit(context.Background(), r.root, r.sh, r.edits, r, "write_file", abs, []byte("x\n")); err == nil {
		t.Fatal("writing through an intermediate symlink must be refused")
	}
	if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
		t.Errorf("temp files survived a failed write: %v", leftovers)
	}
}

// tempLeftovers returns every leftover temporary file beneath root, covering
// both conventions: safefs (.ag-tmp-*) and snap (.ag-*.tmp).
func tempLeftovers(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".ag-tmp-") || (strings.HasPrefix(name, ".ag-") && strings.HasSuffix(name, ".tmp")) {
			out = append(out, p)
		}
		return nil
	})
	return out
}
