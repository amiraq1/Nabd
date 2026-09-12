//go:build android

package safefs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func assertDirNames(t *testing.T, dir string, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := dirNames(t, dir)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("directory %s entries=%v, want %v (temp file left behind?)", dir, got, want)
	}
}

// 1 (and 13): a new file is created byte-for-byte, regular, with the passed
// mode, and no temp file remains.
func TestWriteFileAtomicCreatesFile(t *testing.T) {
	root := t.TempDir()
	data := []byte("hello atomic\n")

	if err := WriteFileAtomic(root, "new.txt", data, 0o640); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("content=%q, want %q", got, data)
	}
	fi, err := os.Stat(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatal("target is not a regular file")
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode=%04o, want 0640", got)
	}
	assertDirNames(t, root, "new.txt")
}

// 2: missing parents are created with the inherited mode; the file keeps its
// own mode.
func TestWriteFileAtomicCreatesParentsWithInheritedMode(t *testing.T) {
	root := t.TempDir() // 0o700

	err := WriteFileAtomic(root, filepath.Join("a", "b", "file.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	for _, d := range []string{
		filepath.Join(root, "a"),
		filepath.Join(root, "a", "b"),
	} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o700 {
			t.Fatalf("%s mode=%04o, want 0700 (inherited)", d, got)
		}
	}
	fi, err := os.Stat(filepath.Join(root, "a", "b", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode=%04o, want 0600", got)
	}
}

// 3: the file mode must not be narrowed by the process umask.
func TestWriteFileAtomicModeIgnoresUmask(t *testing.T) {
	root := t.TempDir()

	old := unix.Umask(0o077)
	defer unix.Umask(old)

	if err := WriteFileAtomic(root, "f.txt", []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	fi, err := os.Stat(filepath.Join(root, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode=%04o, want 0644 (fchmod, not umask-masked)", got)
	}
}

// 4: an existing file is replaced atomically with the new mode.
func TestWriteFileAtomicReplacesExistingFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "f.txt")
	mustWrite(t, target, "old content that must vanish\n")

	if err := WriteFileAtomic(root, "f.txt", []byte("brand new\n"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "brand new\n" {
		t.Fatalf("content=%q", got)
	}
	if bytes.Contains(got, []byte("old content")) {
		t.Fatal("old content survived the replace")
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%04o, want 0600", got)
	}
	assertDirNames(t, root, "f.txt")
}

// 5: a final symlink is replaced, not followed: the external file is untouched
// and the link entry becomes a regular file with the new content.
func TestWriteFileAtomicReplacesFinalSymlinkWithoutFollowing(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustWrite(t, filepath.Join(outside, "external.txt"), "external original")
	mustWrite(t, filepath.Join(root, ".keep"), "")
	mustSymlink(t, filepath.Join(outside, "external.txt"), filepath.Join(root, "link.txt"))

	const payload = "written through atomic rename\n"
	if err := WriteFileAtomic(root, "link.txt", []byte(payload), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	ext, err := os.ReadFile(filepath.Join(outside, "external.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ext) != "external original" {
		t.Fatalf("external file changed: %q", ext)
	}

	li, err := os.Lstat(filepath.Join(root, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if li.Mode()&os.ModeSymlink != 0 {
		t.Fatal("symlink entry survived the replace")
	}
	if !li.Mode().IsRegular() {
		t.Fatal("replaced entry is not a regular file")
	}
	got, err := os.ReadFile(filepath.Join(root, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("content=%q, want %q", got, payload)
	}
}

// 6: an intermediate symlink is refused and nothing is created outside root.
func TestWriteFileAtomicRefusesIntermediateSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustWrite(t, filepath.Join(root, ".keep"), "")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, filepath.Join(root, "vendor"))

	err := WriteFileAtomic(root, filepath.Join("vendor", "x.txt"), []byte("x"), 0o644)
	if !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("err=%v, want ErrNotDirectory", err)
	}
	if _, serr := os.Stat(filepath.Join(outside, "x.txt")); !os.IsNotExist(serr) {
		t.Fatalf("file was created outside root (err=%v)", serr)
	}
	assertDirNames(t, root, ".keep", "vendor")
}

// 7: a regular file used as a parent is refused and left untouched.
func TestWriteFileAtomicRefusesRegularParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.txt")
	mustWrite(t, parent, "parent original")

	err := WriteFileAtomic(root, filepath.Join("parent.txt", "child.txt"), []byte("x"), 0o644)
	if !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("err=%v, want ErrNotDirectory", err)
	}
	got, err := os.ReadFile(parent)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "parent original" {
		t.Fatalf("parent changed: %q", got)
	}
}

// 8: a directory target fails and the temporary file is cleaned up.
func TestWriteFileAtomicDirectoryTargetCleansTemporaryFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := dirNames(t, root)

	if err := WriteFileAtomic(root, "dir", []byte("x"), 0o644); err == nil {
		t.Fatal("expected failure when the target is a directory")
	}

	after := dirNames(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("parent contents changed: %v -> %v (temp file left behind?)", before, after)
	}
}

// 9: a publish failure surfaces the error, keeps the old target, unlinks the
// temp, and leaks no descriptor.
func TestWriteFileAtomicRenameFailureUnlinksTemporaryFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "f.txt")
	mustWrite(t, target, "old")

	real := renameat
	t.Cleanup(func() { renameat = real })
	sentinel := errors.New("synthetic rename failure")
	renameat = func(int, string, int, string) error { return sentinel }

	before, ok := countOpenFDs()

	err := WriteFileAtomic(root, "f.txt", []byte("new"), 0o600)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v, want the synthetic rename error", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("target changed: %q", got)
	}
	assertDirNames(t, root, "f.txt")

	if ok {
		after, _ := countOpenFDs()
		if after != before {
			t.Fatalf("descriptor leak: %d -> %d", before, after)
		}
	}
}

// 10: writeAll retries EINTR and keeps writing through short writes, passing
// every byte exactly once.
func TestWriteAllHandlesShortWritesAndEINTR(t *testing.T) {
	real := writeFD
	t.Cleanup(func() { writeFD = real })

	data := []byte("abcdefghij")
	written := make([]byte, 0, len(data))
	calls := 0
	writeFD = func(_ int, p []byte) (int, error) {
		calls++
		if calls == 1 {
			return 0, unix.EINTR
		}
		n := len(p)
		if n > 3 {
			n = 3
		}
		written = append(written, p[:n]...)
		return n, nil
	}

	if err := writeAll(-1, data); err != nil {
		t.Fatalf("writeAll: %v", err)
	}
	if !bytes.Equal(written, data) {
		t.Fatalf("written=%q, want %q", written, data)
	}
	if calls < 2 {
		t.Fatalf("expected an EINTR retry; calls=%d", calls)
	}
}

// 11: the file is fsynced before renameat, and the parent is fsynced after.
func TestWriteFileAtomicSyncsFileBeforeRenameAndParentAfterRename(t *testing.T) {
	root := t.TempDir()

	realFsync := fsyncFD
	realRename := renameat
	t.Cleanup(func() {
		fsyncFD = realFsync
		renameat = realRename
	})

	var events []string
	fsyncFD = func(fd int) error {
		events = append(events, "fsync")
		return realFsync(fd)
	}
	renameat = func(od int, op string, nd int, np string) error {
		events = append(events, "renameat")
		return realRename(od, op, nd, np)
	}

	if err := WriteFileAtomic(root, "f.txt", []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	want := []string{"fsync", "renameat", "fsync"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("event order=%v, want %v (file-fsync, renameat, parent-fsync)", events, want)
	}
}

// 12: invalid paths are rejected, including "." and a trailing separator,
// which name a directory rather than a file.
func TestWriteFileAtomicRejectsInvalidPaths(t *testing.T) {
	root := t.TempDir()

	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrEmptyPath},
		{"nul", "safe" + string(rune(0)) + "x", ErrNULPath},
		{"absolute", string(filepath.Separator) + "etc/passwd", ErrAbsolutePath},
		{"traversal", ".." + string(filepath.Separator) + "x", ErrTraversal},
		{"dot", ".", ErrInvalidTarget},
		{"trailing separator", "dir" + string(filepath.Separator), ErrInvalidTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := WriteFileAtomic(root, tc.in, []byte("x"), 0o644); !errors.Is(err, tc.want) {
				t.Fatalf("WriteFileAtomic(%q) err=%v, want %v", tc.in, err, tc.want)
			}
		})
	}
}

// 14: repeated failing publishes leak no descriptors.
func TestWriteFileAtomicNoDescriptorLeakOnFailure(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "parent.txt"), "x")

	before, ok := countOpenFDs()
	if !ok {
		t.Skip("cannot read /proc/self/fd on this platform")
	}

	for i := 0; i < 64; i++ {
		err := WriteFileAtomic(root, filepath.Join("parent.txt", "c.txt"), []byte("x"), 0o644)
		if err == nil {
			t.Fatal("expected a failure through a regular-file parent")
		}
	}

	after, _ := countOpenFDs()
	if after != before {
		t.Fatalf("descriptor leak: %d -> %d", before, after)
	}
}

// 15: a concurrent reader must only ever observe a complete payload, never a
// partial write or a mix of the two.
func TestWriteFileAtomicNeverExposesPartialContent(t *testing.T) {
	root := t.TempDir()
	const size = 64 * 1024
	a := bytes.Repeat([]byte("A"), size)
	b := bytes.Repeat([]byte("B"), size)
	target := filepath.Join(root, "f.bin")

	if err := WriteFileAtomic(root, "f.bin", a, 0o644); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			got, err := os.ReadFile(target)
			if err != nil {
				continue // both old and new names point at complete inodes
			}
			if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
				t.Errorf("partial/mixed content observed: len=%d", len(got))
				return
			}
		}
	}()

	for i := 0; i < 200; i++ {
		payload := a
		if i%2 == 1 {
			payload = b
		}
		if err := WriteFileAtomic(root, "f.bin", payload, 0o644); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	close(done)
	wg.Wait()
}
