//go:build windows
// +build windows

package config

import "os"

// checkOwnerPlatform (Windows / non-Unix) is a no-op: NT ACL semantics make a
// numeric-uid ownership check meaningless, and ACLs are enforced by the OS.
func checkOwnerPlatform(p string, fi os.FileInfo) error {
	return nil
}
