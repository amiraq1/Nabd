//go:build windows

package registry

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockExclusive(f *os.File) error {
	// Lock the first byte exclusively, blocking until acquired.
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ol)
}

func unlockExclusive(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
