package safefs

import (
	"os"
	"strings"
	"testing"
)

// The unix removal must be built from descriptor-relative operations: the
// parent is opened (never created) and the base is unlinked relative to that
// descriptor. A path-based os.Remove would reintroduce the race.
func TestRemoveUnixUsesDescriptorOnlyOperations(t *testing.T) {
	src, err := os.ReadFile("remove_unix.go")
	if err != nil {
		t.Fatalf("remove_unix.go: %v", err)
	}
	body := string(src)

	for _, tok := range []string{"Unlinkat", "OpenDir", "splitFileTarget"} {
		if !strings.Contains(body, tok) {
			t.Errorf("remove_unix.go must use %s", tok)
		}
	}
	for _, tok := range []string{"os.Remove(", "os.RemoveAll(", "os.MkdirAll("} {
		if strings.Contains(body, tok) {
			t.Errorf("remove_unix.go must not call %s", tok)
		}
	}
}

// The non-unix implementation is fail-closed: it must not delete anything.
func TestRemoveOtherIsFailClosed(t *testing.T) {
	src, err := os.ReadFile("remove_other.go")
	if err != nil {
		t.Fatalf("remove_other.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, "//go:build !unix") {
		t.Error("remove_other.go must carry the //go:build !unix constraint")
	}
	if !strings.Contains(body, "ErrUnsupportedPlatform") {
		t.Error("remove_other.go must be fail-closed with ErrUnsupportedPlatform")
	}
	for _, tok := range []string{"os.Remove(", "golang.org/x/sys/unix", "unix.Unlinkat"} {
		if strings.Contains(body, tok) {
			t.Errorf("remove_other.go must not use %s", tok)
		}
	}
}
