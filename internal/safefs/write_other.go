//go:build !android

package safefs

import "os"

// WriteFileAtomic is fail-closed on every platform other than Android/Termux.
// It performs no open, write, or rename.
func WriteFileAtomic(rootPath, relativeFile string, data []byte, mode os.FileMode) error {
	return ErrUnsupportedPlatform
}
