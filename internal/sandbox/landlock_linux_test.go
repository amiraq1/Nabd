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

	"golang.org/x/sys/unix"
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
	if os.Getenv("NABD_LANDLOCK_NETWORK_HELPER") == "1" {
		root := os.Getenv("NABD_LANDLOCK_TEST_ROOT")
		if err := Apply(Config{Root: root, Writable: []string{root}, DenyNetwork: true}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "network socket error=%v\n", err)
			os.Exit(3)
		}
		defer syscall.Close(fd)
		sa := &syscall.SockaddrInet4{Port: 34567, Addr: [4]byte{127, 0, 0, 1}}
		err = syscall.Bind(fd, sa)
		if err == nil {
			fmt.Fprintln(os.Stderr, "network bind unexpectedly allowed")
			os.Exit(4)
		}
		if !errors.Is(err, syscall.EACCES) && !errors.Is(err, syscall.EPERM) {
			fmt.Fprintf(os.Stderr, "network bind error=%v, want permission denial\n", err)
			os.Exit(5)
		}
		os.Exit(0)
	}
	if os.Getenv("NABD_LANDLOCK_RESOURCE_HELPER") == "1" {
		root := os.Getenv("NABD_LANDLOCK_TEST_ROOT")
		if err := Apply(Config{Root: root, Writable: []string{root}, LimitResources: true}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		var cpu, files unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_CPU, &cpu); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &files); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(4)
		}
		if cpu.Cur != resourceCPUSeconds || cpu.Max != resourceCPUSeconds {
			fmt.Fprintf(os.Stderr, "cpu limit=%+v, want %d\n", cpu, resourceCPUSeconds)
			os.Exit(5)
		}
		if files.Cur != resourceOpenFileCount || files.Max != resourceOpenFileCount {
			fmt.Fprintf(os.Stderr, "open-file limit=%+v, want %d\n", files, resourceOpenFileCount)
			os.Exit(6)
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

func TestLandlockDeniesNetwork(t *testing.T) {
	if !SupportsNetwork() {
		t.Skip("Landlock network restrictions are unavailable on this kernel")
	}
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockNetworkHelper")
	cmd.Env = append(os.Environ(),
		"NABD_LANDLOCK_NETWORK_HELPER=1",
		"NABD_LANDLOCK_TEST_ROOT="+root,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Landlock network helper failed: %v\n%s", err, out)
	}
}

func TestLandlockAppliesResourceLimits(t *testing.T) {
	if !Available() {
		t.Skip("Landlock is unavailable on this kernel")
	}
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockResourceHelper")
	cmd.Env = append(os.Environ(),
		"NABD_LANDLOCK_RESOURCE_HELPER=1",
		"NABD_LANDLOCK_TEST_ROOT="+root,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Landlock resource helper failed: %v\n%s", err, out)
	}
}
