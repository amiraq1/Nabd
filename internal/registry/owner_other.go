//go:build windows

package registry

import "os"

func checkOwner(p string, fi os.FileInfo) error {
	return nil
}
