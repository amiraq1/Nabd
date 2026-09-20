//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("NABD_LANDLOCK_TEST_HELPER") == "1" {
		root := os.Getenv("NABD_LANDLOCK_TEST_ROOT")
		outside := os.Getenv("NABD_LANDLOCK_TEST_OUTSIDE")
		if err := Apply(Config{Root: root, Writable: []string{root}}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := os.WriteFile(filepath.Join(root, "inside"), []byte("ok"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		err := os.WriteFile(filepath.Join(outside, "outside"), []byte("no"), 0o600)
		if err == nil || (!errors.Is(err, syscall.EACCES) && !errors.Is(err, syscall.EPERM)) {
			fmt.Fprintf(os.Stderr, "outside write error=%v, want permission denial\n", err)
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLandlockRestrictsOutsideRoot(t *testing.T) {
	if !Available() {
		t.Skip("Landlock is unavailable on this kernel")
	}
	root := t.TempDir()
	outside := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockHelper")
	cmd.Env = append(os.Environ(),
		"NABD_LANDLOCK_TEST_HELPER=1",
		"NABD_LANDLOCK_TEST_ROOT="+root,
		"NABD_LANDLOCK_TEST_OUTSIDE="+outside,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Landlock helper failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "inside")); err != nil {
		t.Fatalf("sandbox denied writable root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "outside")); !os.IsNotExist(err) {
		t.Fatalf("sandbox allowed outside write: err=%v", err)
	}
}
