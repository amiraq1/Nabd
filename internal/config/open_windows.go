//go:build windows

package config

import (
	"fmt"
	"os"
)

func openConfigFile(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%s: symlink not allowed", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !os.SameFile(before, after) {
		f.Close()
		return nil, nil, fmt.Errorf("%s: file changed while opening", path)
	}
	return f, after, nil
}
