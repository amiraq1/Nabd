//go:build !linux && !android && !windows

package snap

import (
	"errors"
	"os"
)

func renameNoReplace(oldpath, newpath string) error {
	err := os.Link(oldpath, newpath)
	if err != nil {
		if os.IsExist(err) {
			return err
		}
		// Any other link error (permission denied, cross-device, not supported)
		// means atomic publish via link is unsupported.
		return errors.Join(ErrAtomicPublishUnsupported, err)
	}
	os.Remove(oldpath)
	return nil
}
