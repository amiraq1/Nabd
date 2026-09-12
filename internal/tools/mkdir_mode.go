package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

// mkdirParentDirs creates missing directories with the nearest existing
// ancestor's permission bits. The 0755 fallback is reachable only when
// no usable existing ancestor can be found.
func mkdirParentDirs(abs string) error {
	target := filepath.Dir(abs)
	if fi, err := os.Stat(target); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("parent is not a directory: %s", target)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	ancestor := target
	for {
		parent := filepath.Dir(ancestor)
		fi, err := os.Stat(parent)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("ancestor is not a directory: %s", parent)
			}
			mode := fi.Mode().Perm()
			if mode == 0 {
				mode = 0o755
			}
			return os.MkdirAll(target, mode)
		}
		if !os.IsNotExist(err) {
			return err
		}
		if parent == ancestor {
			return os.MkdirAll(target, 0o755)
		}
		ancestor = parent
	}
}
