package safefs

import (
	"os"
	"strings"
	"testing"
)

// The unix write primitive must be built only from descriptor-relative
// syscalls; a path-based shortcut (CreateTemp/WriteFile/Rename/MkdirAll) would
// reintroduce the very race this work removes.
func TestWriteUnixUsesDescriptorOnlyOperations(t *testing.T) {
	src, err := os.ReadFile("write_unix.go")
	if err != nil {
		t.Fatalf("write_unix.go: %v", err)
	}
	body := string(src)

	required := []string{
		"Openat", "Fchmod", "Fsync", "Renameat", "Unlinkat",
		"O_CREAT", "O_EXCL", "O_CLOEXEC", "O_NOFOLLOW",
	}
	for _, tok := range required {
		if !strings.Contains(body, tok) {
			t.Errorf("write_unix.go must use %s", tok)
		}
	}

	banned := []string{
		"os.CreateTemp", "os.WriteFile", "os.Rename", "os.MkdirAll",
		"filepath.EvalSymlinks",
	}
	for _, tok := range banned {
		if strings.Contains(body, tok) {
			t.Errorf("write_unix.go must not use %s", tok)
		}
	}
}
