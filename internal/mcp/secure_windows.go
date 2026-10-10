//go:build windows

package mcp

import "errors"

// readSecureFile refuses clearly on Windows: the O_NOFOLLOW/owner guarantees
// this package relies on have no Windows counterpart, so MCP secure config
// loading is unsupported there rather than silently weaker.
func readSecureFile(path string, limit int64) ([]byte, error) {
	return nil, errors.New("mcp: secure config loading is not supported on Windows")
}
