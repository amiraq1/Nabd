package snap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestGitignoreWriteFailureDoesNotBlockTheEdit: the shield exists for the
// user's safety, but it must never be the reason an edit fails. With the
// shield write forced to fail, the capture — and therefore the edit it
// records — still stores its blob; the file is simply left absent for a later
// write-open to create.
func TestGitignoreWriteFailureDoesNotBlockTheEdit(t *testing.T) {
	old := ensureGitignoreFn
	ensureGitignoreFn = func(string) error { return errors.New("injected shield write failure") }
	t.Cleanup(func() { ensureGitignoreFn = old })

	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "file.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := s.Capture(target)
	if err != nil {
		t.Fatalf("a failed shield write blocked the edit: %v", err)
	}
	if st.Blob == "" {
		t.Fatal("blob was not stored")
	}
	if _, err := os.Stat(filepath.Join(root, ".ag", ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("injected failure should leave the file absent, stat err=%v", err)
	}
}
