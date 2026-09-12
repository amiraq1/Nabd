package safefs

import (
	"os"
	"strings"
	"testing"
)

// 12: the non-unix implementation is fail-closed and defines both entry
// points, so the package compiles everywhere without a path-based fallback.
func TestOpenDirOtherIsFailClosed(t *testing.T) {
	src, err := os.ReadFile("open_dir_other.go")
	if err != nil {
		t.Fatalf("open_dir_other.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, "//go:build !unix") {
		t.Error("open_dir_other.go must carry the //go:build !unix constraint")
	}
	if !strings.Contains(body, "ErrUnsupportedPlatform") {
		t.Error("open_dir_other.go must be fail-closed with ErrUnsupportedPlatform")
	}
	if !strings.Contains(body, "func OpenDir(") {
		t.Error("open_dir_other.go must define OpenDir")
	}
	if !strings.Contains(body, "func OpenOrCreateDir(") {
		t.Error("open_dir_other.go must define OpenOrCreateDir")
	}
}
