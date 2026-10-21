//go:build windows

package store

import "os"

// openFileNoFollow opens path. Windows has no O_NOFOLLOW equivalent for
// open(2); symlink planting at the journal path is instead mitigated by the
// 0700 session directory and the up-front symlink rejection in
// NewJSONLWithOptions.
func openFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}

// openAppendFile opens path for appending, creating it with 0600 when absent.
func openAppendFile(path string) (*os.File, error) {
	return openFileNoFollow(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
