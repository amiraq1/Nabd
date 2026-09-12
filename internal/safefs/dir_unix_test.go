//go:build unix

package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// 1: "." opens the anchored root itself.
func TestOpenDirRoot(t *testing.T) {
	root := t.TempDir()

	f, err := OpenDir(root, ".")
	if err != nil {
		t.Fatalf("OpenDir(.): %v", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatal("descriptor is not a directory")
	}
}

// 2: an existing directory opens.
func TestOpenDirExisting(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}

	f, err := OpenDir(root, filepath.Join("a", "b"))
	if err != nil {
		t.Fatalf("OpenDir: %v", err)
	}
	defer f.Close()

	if fi, _ := f.Stat(); !fi.IsDir() {
		t.Fatal("descriptor is not a directory")
	}
}

func TestOpenDirMissingIsNotExist(t *testing.T) {
	root := t.TempDir()

	if _, err := OpenDir(root, "nope"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing dir err=%v, want ErrNotExist", err)
	}
}

// 3: a missing chain is created.
func TestOpenOrCreateDirCreatesChain(t *testing.T) {
	root := t.TempDir()

	f, err := OpenOrCreateDir(root, filepath.Join("one", "two", "three"), 0o755)
	if err != nil {
		t.Fatalf("OpenOrCreateDir: %v", err)
	}
	defer f.Close()

	// 10: the returned descriptor is the final directory.
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatal("returned descriptor is not a directory")
	}

	st, err := os.Stat(filepath.Join(root, "one", "two", "three"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Fatal("final directory was not created")
	}
}

// 4: created directories inherit the nearest existing ancestor's mode.
func TestOpenOrCreateDirInheritsAncestorMode(t *testing.T) {
	root := t.TempDir() // t.TempDir() is 0o700 on this platform

	f, err := OpenOrCreateDir(root, filepath.Join("p", "q"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	for _, d := range []string{
		filepath.Join(root, "p"),
		filepath.Join(root, "p", "q"),
	} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o700 {
			t.Fatalf("%s mode=%04o, want 0700 (inherited from root)", d, got)
		}
	}
}

// 5: the created mode is applied with fchmod, so a restrictive umask cannot
// narrow it.
func TestOpenOrCreateDirModeUnaffectedByUmask(t *testing.T) {
	root := t.TempDir()
	pub := filepath.Join(root, "pub")
	if err := os.Mkdir(pub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pub, 0o755); err != nil {
		t.Fatal(err)
	}

	old := unix.Umask(0o077)
	defer unix.Umask(old)

	f, err := OpenOrCreateDir(root, filepath.Join("pub", "x"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fi, err := os.Stat(filepath.Join(root, "pub", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode=%04o, want 0755 (fchmod, not umask-masked)", got)
	}
}

// 6: an intermediate symlink is refused. With O_NOFOLLOW|O_DIRECTORY Android
// reports ENOTDIR (not ELOOP), so this is ErrNotDirectory, consistent with the
// read path.
func TestOpenDirRefusesIntermediateSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustSymlink(t, outside, filepath.Join(root, "vendor"))

	if _, err := OpenDir(root, filepath.Join("vendor", "sub")); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("intermediate symlink err=%v, want ErrNotDirectory", err)
	}
}

// 7: a final symlink is refused. OpenDir opens the final component with
// O_DIRECTORY as well, so a symlink surfaces as ENOTDIR -> ErrNotDirectory.
func TestOpenDirRefusesFinalSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, ".keep"), "")
	mustSymlink(t, outside, filepath.Join(root, "link"))

	if _, err := OpenDir(root, "link"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("final symlink err=%v, want ErrNotDirectory", err)
	}
}

// 8: a regular file used as an intermediate component is refused.
func TestOpenDirRefusesRegularFileComponent(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "f.txt"), "x")

	if _, err := OpenDir(root, filepath.Join("f.txt", "sub")); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("regular file component err=%v, want ErrNotDirectory", err)
	}
}

// 9: nothing is created outside root when a component is a symlink.
func TestOpenOrCreateDirDoesNotCreateOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, ".keep"), "")
	mustSymlink(t, outside, filepath.Join(root, "vendor"))

	if _, err := OpenOrCreateDir(root, filepath.Join("vendor", "newdir"), 0o755); err == nil {
		t.Fatal("expected refusal through a symlinked component")
	}
	if _, err := os.Stat(filepath.Join(outside, "newdir")); !os.IsNotExist(err) {
		t.Fatalf("directory was created outside root (err=%v)", err)
	}
}

// 11: a failing open leaks no descriptors.
func TestOpenDirNoDescriptorLeakOnFailure(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, ".keep"), "")
	mustSymlink(t, outside, filepath.Join(root, "vendor"))

	before, ok := countOpenFDs()
	if !ok {
		t.Skip("cannot read /proc/self/fd on this platform")
	}

	for i := 0; i < 64; i++ {
		if _, err := OpenDir(root, filepath.Join("vendor", "sub")); err == nil {
			t.Fatal("expected a failure through the symlinked component")
		}
	}

	after, _ := countOpenFDs()
	if after != before {
		t.Fatalf("descriptor leak: %d -> %d", before, after)
	}
}

func countOpenFDs() (int, bool) {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return len(ents), true
}

// A directory that wins the ENOENT -> mkdirat race belongs to another writer.
// OpenOrCreateDir must open it but must never chmod it, or it would alter the
// permissions of a directory this process does not own.
func TestOpenOrCreateDirDoesNotChmodConcurrentWinner(t *testing.T) {
	root := t.TempDir() // 0700; without the guard the winner would be forced to 0700

	real := mkdirat
	t.Cleanup(func() { mkdirat = real })
	mkdirat = func(dirfd int, path string, mode uint32) error {
		// The concurrent creator wins with its own mode...
		if err := real(dirfd, path, 0o711); err != nil {
			return err
		}
		// ...applied explicitly, because the process umask would otherwise
		// narrow 0711 (this environment runs with umask 0077).
		cfd, err := unix.Openat(dirfd, path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		if cerr := unix.Fchmod(cfd, 0o711); cerr != nil {
			unix.Close(cfd)
			return cerr
		}
		unix.Close(cfd)
		// ...so the implementation's own mkdirat now observes EEXIST.
		return real(dirfd, path, mode)
	}

	f, err := OpenOrCreateDir(root, "child", 0o755)
	if err != nil {
		t.Fatalf("OpenOrCreateDir: %v", err)
	}
	defer f.Close()

	dir := filepath.Join(root, "child")
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o711 {
		t.Fatalf("concurrent winner mode=%04o, want 0711 (must not be chmodded)", got)
	}

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(fi, st) {
		t.Fatal("returned descriptor does not refer to the winner directory")
	}
}
