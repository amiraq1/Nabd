package shadow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return store, dir
}

func metaCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".nabd", "shadow"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), metaSuffix) {
			count++
		}
	}
	return count
}

func TestOpenDoesNotWrite(t *testing.T) {
	_, dir := newTestStore(t)
	if _, err := os.Stat(filepath.Join(dir, ".nabd")); !os.IsNotExist(err) {
		t.Fatalf("Open created .nabd eagerly (err=%v)", err)
	}
}

func TestBackupAndLatest(t *testing.T) {
	store, dir := newTestStore(t)
	before := []byte("original\n")
	after := []byte("changed\n")

	entry, err := store.Backup("src/main.go", before, 0o640, after)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID == "" || entry.Path != "src/main.go" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(dir, ".nabd", "shadow", entry.ID+backupExt)); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".nabd", "shadow", entry.ID+metaSuffix)); err != nil {
		t.Fatalf("metadata file missing: %v", err)
	}

	got, data, err := store.Latest("src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != entry.ID || string(data) != string(before) {
		t.Fatalf("Latest = %+v / %q", got, data)
	}
	if _, _, err := store.Latest("other.go"); err != ErrNotFound {
		t.Fatalf("Latest(other) error = %v, want ErrNotFound", err)
	}
	if _, _, err := store.Latest(""); err != nil {
		t.Fatalf("Latest(any) error = %v", err)
	}
}

func TestMarkUndoneHidesEntry(t *testing.T) {
	store, _ := newTestStore(t)
	entry, err := store.Backup("a.txt", []byte("before"), 0o644, []byte("after"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkUndone(entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Latest("a.txt"); err != ErrNotFound {
		t.Fatalf("Latest after undo = %v, want ErrNotFound", err)
	}
}

func TestLatestRejectsCorruptBackup(t *testing.T) {
	store, dir := newTestStore(t)
	entry, err := store.Backup("a.txt", []byte("before"), 0o644, []byte("after"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".nabd", "shadow", entry.ID+backupExt)
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Latest("a.txt"); err == nil {
		t.Fatal("corrupt backup was accepted")
	}
}

func TestRetentionKeepsLatestTwenty(t *testing.T) {
	store, _ := newTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	store.now = func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Minute)
	}
	for i := 0; i < 25; i++ {
		if _, err := store.Backup("f.txt", []byte{byte(i)}, 0o644, []byte{byte(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := metaCount(t, store.root.Name()); got != 20 {
		t.Fatalf("retained %d entries, want 20", got)
	}
}

func TestRetentionDeletesExpiredEntries(t *testing.T) {
	store, dir := newTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return base }
	if _, err := store.Backup("f.txt", []byte("old"), 0o644, []byte("new")); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return base.Add(8 * 24 * time.Hour) }
	if _, err := store.Backup("f.txt", []byte("new"), 0o644, []byte("newer")); err != nil {
		t.Fatal(err)
	}
	if got := metaCount(t, dir); got != 1 {
		t.Fatalf("retained %d entries, want 1", got)
	}
}
