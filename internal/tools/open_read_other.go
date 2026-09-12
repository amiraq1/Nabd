//go:build !unix

package tools

import "os"

// openReadFromRoot is the COMPATIBILITY PATH used on platforms where the
// descriptor-relative open (internal/safefs) is unavailable.
//
// It resolves the path with Root.Resolve and then opens it by path. That is the
// resolve-then-open pattern this project is removing on Android, so this
// function is a compatibility path only and NOT a safe fallback: it preserves
// existing behaviour for non-Android builds and carries no new TOCTOU
// guarantee. New confinement-sensitive code must not rely on it.
//
// The returned abs is reporting/accounting metadata only. The opened
// descriptor, not that string, is the authority for all file reads.
func openReadFromRoot(root *Root, relativePath string) (*os.File, string, error) {
	abs, err := root.Resolve(relativePath)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, "", err
	}
	return f, abs, nil
}
