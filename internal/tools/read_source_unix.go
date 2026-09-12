//go:build unix

package tools

import (
	"fmt"
	"io"

	"nabd/internal/safefs"
)

// readSourceFromRoot reads a source file beneath root through a
// descriptor-relative open (safefs.OpenRead) and returns its bytes.
//
// relative is the filesystem authority; absolute is metadata only. The size
// limit is enforced on the descriptor's own stat AND while reading, so the file
// that was measured is provably the file that was read, and a file that grows
// between the two cannot exceed the ceiling.
func readSourceFromRoot(root *Root, relative, absolute string, limit int) ([]byte, error) {
	f, err := safefs.OpenRead(root.Dir(), relative)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > int64(limit) {
		return nil, fmt.Errorf("file is %d bytes, limit is %d", fi.Size(), limit)
	}

	// LimitReader guards the case where the file grew between fstat and read:
	// the ceiling stays a ceiling even under a concurrent append.
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("file grew past the %d byte limit while reading", limit)
	}
	return data, nil
}
