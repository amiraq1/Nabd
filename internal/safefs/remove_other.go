//go:build !unix

package safefs

// RemoveFile is fail-closed on every platform other than unix. It performs no
// filesystem operation.
func RemoveFile(rootPath, relativeFile string) error {
	return ErrUnsupportedPlatform
}
