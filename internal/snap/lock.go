// Package snap: lock.go provides advisory locking for multi-process
// coordination on the .ag directory. The lock is held only for the duration
// of a mutation or an /undo, never for a whole session.
//
// On Android/Termux, flock works on the filesystems we target (ext4, f2fs).
// If it ever doesn't, the caller gets an error rather than silent unsafety.
package snap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrLockHeld is returned when another process holds the lock.
var ErrLockHeld = errors.New("another nabd process holds the lock")

// lockPath returns the path to the advisory lock file for a root.
func lockPath(root string) string {
	return filepath.Join(root, ".ag", "lock")
}

// Lock represents a held advisory lock. Release must be called when done.
type Lock struct {
	f   *os.File
	set bool
}

// Acquire tries to acquire the advisory lock for the given root. It blocks
// until the lock is available or the timeout elapses. On success, it returns
// a Lock that must be released. On contention, it returns ErrLockHeld with a
// message naming the holding pid.
func Acquire(root string, timeout time.Duration) (*Lock, error) {
	lockFile := lockPath(root)
	if err := os.MkdirAll(filepath.Dir(lockFile), 0o755); err != nil {
		return nil, fmt.Errorf("lock: create .ag dir: %w", err)
	}
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock: open: %w", err)
	}

	// Try non-blocking first to detect contention and report the holder.
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		// Write the pidfile while we hold the lock.
		if werr := writePidfile(filepath.Dir(lockFile)); werr != nil {
			syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
			return nil, fmt.Errorf("lock: write pidfile: %w", werr)
		}
		return &Lock{f: f, set: true}, nil
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		f.Close()
		return nil, fmt.Errorf("lock: flock: %w", err)
	}

	// Contended. Try to report who holds it.
	if holder := reportHolder(lockFile); holder != "" {
		f.Close()
		return nil, fmt.Errorf("%w: %s", ErrLockHeld, holder)
	}

	// Holder unknown or stale — wait up to timeout for release.
	deadline := time.Now().Add(timeout)
	for {
		time.Sleep(50 * time.Millisecond)
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f, set: true}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("lock: flock: %w", err)
		}
		if time.Now().After(deadline) {
			if holder := reportHolder(lockFile); holder != "" {
				f.Close()
				return nil, fmt.Errorf("%w (timeout): %s", ErrLockHeld, holder)
			}
			f.Close()
			return nil, fmt.Errorf("%w (timeout): unknown holder", ErrLockHeld)
		}
	}
}

// reportHolder tries to read the pid of the process holding the lock file.
// It returns a human-readable string or "" if the holder can't be determined.
func reportHolder(lockFile string) string {
	// flock doesn't directly tell us who holds it. We rely on a companion
	// pidfile written alongside the lock (see pidfile.go). Fall back to
	// "pid unknown" if no pidfile exists.
	pidFile := lockFile + ".pid"
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return "pid unknown"
	}
	pid := string(b)
	// Check if the pid is still alive.
	if !pidAlive(pid) {
		return fmt.Sprintf("pid %s (stale)", pid)
	}
	return fmt.Sprintf("pid %s", pid)
}

// pidAlive reports whether a process with the given pid exists.
func pidAlive(pid string) bool {
	_, err := os.Stat(filepath.Join("/proc", pid))
	return err == nil
}

// Release releases the lock. Safe to call multiple times.
func (l *Lock) Release() error {
	if !l.set || l.f == nil {
		return nil
	}
	l.set = false
	// Remove the companion pidfile on release.
	lockFile := l.f.Name()
	os.Remove(lockFile + ".pid")
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
