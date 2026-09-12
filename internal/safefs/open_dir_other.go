//go:build !unix

package safefs

import "os"

// OpenDir is fail-closed on every platform other than unix. It
// performs no filesystem lookup at all.
func OpenDir(rootPath, relativeDir string) (*os.File, error) {
	return nil, ErrUnsupportedPlatform
}

// OpenOrCreateDir is fail-closed on every platform other than unix.
// It performs no filesystem lookup at all.
func OpenOrCreateDir(rootPath, relativeDir string, fallbackMode os.FileMode) (*os.File, error) {
	return nil, ErrUnsupportedPlatform
}
