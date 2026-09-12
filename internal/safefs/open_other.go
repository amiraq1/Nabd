//go:build !unix

package safefs

import "os"

// OpenRead is fail-closed on every platform other than unix.
//
// It deliberately does NOT fall back to a path-based open. This package exists
// to provide descriptor-relative confinement; a path-based fallback would
// silently re-introduce the resolve-then-open race and make the package name a
// promise the implementation does not keep. Off-unix callers that must keep
// working use their own compatibility path elsewhere, documented as such.
func OpenRead(rootPath, relativePath string) (*os.File, error) {
	return nil, ErrUnsupportedPlatform
}
