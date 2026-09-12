package safefs

import (
	"os"
	"strings"
	"testing"
)

// The Android write primitive must be built only from descriptor-relative
// syscalls; a path-based shortcut (CreateTemp/WriteFile/Rename/MkdirAll) would
// reintroduce the very race this work removes.
func TestWriteAndroidUsesDescriptorOnlyOperations(t *testing.T) {
	src, err := os.ReadFile("write_android.go")
	if err != nil {
		t.Fatalf("write_android.go: %v", err)
	}
	body := string(src)

	required := []string{
		"Openat", "Fchmod", "Fsync", "Renameat", "Unlinkat",
		"O_CREAT", "O_EXCL", "O_CLOEXEC", "O_NOFOLLOW",
	}
	for _, tok := range required {
		if !strings.Contains(body, tok) {
			t.Errorf("write_android.go must use %s", tok)
		}
	}

	banned := []string{
		"os.CreateTemp", "os.WriteFile", "os.Rename", "os.MkdirAll",
		"filepath.EvalSymlinks",
	}
	for _, tok := range banned {
		if strings.Contains(body, tok) {
			t.Errorf("write_android.go must not use %s", tok)
		}
	}
}

// The non-Android implementation is fail-closed: it must not open, write, or
// rename anything, directly or through the shadow store.
func TestWriteOtherIsFailClosed(t *testing.T) {
	src, err := os.ReadFile("write_other.go")
	if err != nil {
		t.Fatalf("write_other.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, "//go:build !android") {
		t.Error("write_other.go must carry the //go:build !android constraint")
	}
	if !strings.Contains(body, "ErrUnsupportedPlatform") {
		t.Error("write_other.go must be fail-closed with ErrUnsupportedPlatform")
	}
	if !strings.Contains(body, "func WriteFileAtomic(") {
		t.Error("write_other.go must define WriteFileAtomic")
	}
	for _, tok := range []string{"os.Open(", "os.WriteFile", "os.Rename", "snap.WriteAtomic"} {
		if strings.Contains(body, tok) {
			t.Errorf("write_other.go must not use %s", tok)
		}
	}
}
