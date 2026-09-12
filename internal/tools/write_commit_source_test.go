package tools

import (
	"os"
	"strings"
	"testing"
)

// T2d structural contract: the shared mutation tail delegates to the platform
// adapters and never opens, stats, or writes a project file by absolute path.
func TestWriteCommitDelegatesToAdapters(t *testing.T) {
	body := readCommitSource(t, "write_commit.go")

	for _, want := range []string{"writePathFromRoot", "captureFromRoot", "writeFromRoot"} {
		if !strings.Contains(body, want) {
			t.Errorf("write_commit.go must use %s", want)
		}
	}
	for _, banned := range []string{
		"sh.Capture(abs)",
		"snap.WriteAtomic(abs",
		"mkdirParentDirs(abs)",
		"os.Stat(",
		"os.ReadFile(",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("write_commit.go must not contain %q; mutations go through the adapters", banned)
		}
	}
}

// The Android adapters are descriptor-relative only: the relative path is the
// authority, the absolute path is reporting metadata, and no path-based
// shortcut may reappear.
func TestWriteCommitUnixAdapterIsDescriptorOnly(t *testing.T) {
	body := readCommitSource(t, "write_commit_unix.go")

	for _, want := range []string{"safefs.OpenRead", "safefs.WriteFileAtomic"} {
		if !strings.Contains(body, want) {
			t.Errorf("write_commit_unix.go must use %s", want)
		}
	}
	for _, banned := range []string{
		"Root.Resolve",
		"os.Open",
		"os.ReadFile",
		"snap.WriteAtomic",
		"mkdirParentDirs",
		"EvalSymlinks",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("write_commit_unix.go must not use %s", banned)
		}
	}
}

// The non-Android adapter is the documented compatibility path: it preserves
// existing behaviour with Root.Resolve-derived absolute paths and must not
// reach into the safe-open API.
func TestWriteCommitOtherIsCompatibilityPath(t *testing.T) {
	body := readCommitSource(t, "write_commit_other.go")

	if !strings.Contains(body, "//go:build !unix") {
		t.Error("write_commit_other.go must carry the //go:build !unix constraint")
	}
	if !strings.Contains(body, "compatibility") {
		t.Error("write_commit_other.go must document itself as a compatibility path")
	}
	for _, want := range []string{"sh.Capture(", "mkdirParentDirs(", "snap.WriteAtomic("} {
		if !strings.Contains(body, want) {
			t.Errorf("write_commit_other.go must use %s", want)
		}
	}
	for _, banned := range []string{"safefs.OpenRead", "safefs.WriteFileAtomic"} {
		if strings.Contains(body, banned) {
			t.Errorf("write_commit_other.go is the compatibility path and must not use %s", banned)
		}
	}
}

func readCommitSource(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(src)
}
