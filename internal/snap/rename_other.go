//go:build !linux && !android && !windows

package snap

import (
	"os"
)

func renameNoReplace(oldpath, newpath string) error {
	err := os.Link(oldpath, newpath)
	if err != nil {
		return err
	}
	os.Remove(oldpath)
	return nil
}
