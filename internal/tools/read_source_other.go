//go:build !unix

package tools

import (
	"fmt"
	"os"
)

// readSourceFromRoot is the COMPATIBILITY PATH used on platforms where the
// descriptor-relative open in internal/safefs is unavailable. It measures and
// reads by absolute path and carries no new TOCTOU guarantee.
func readSourceFromRoot(root *Root, relative, absolute string, limit int) ([]byte, error) {
	st, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if st.Size() > int64(limit) {
		return nil, fmt.Errorf("file is %d bytes, limit is %d", st.Size(), limit)
	}
	return os.ReadFile(absolute)
}
