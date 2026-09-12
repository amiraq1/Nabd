package tools

import (
	"os"
	"strings"
	"testing"
)

// 9: read_file must obtain type/size from the open descriptor, never from a
// second path lookup. os.Stat/os.Open on a path is exactly the resolve-then-open
// pattern the Android adapter removes.
func TestReadFileUsesDescriptorStat(t *testing.T) {
	src, err := os.ReadFile("read.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	for _, banned := range []string{"os.Stat(", "os.Open("} {
		if strings.Contains(body, banned) {
			t.Errorf("read.go still calls %s; reads must go through the descriptor", banned)
		}
	}
	if !strings.Contains(body, "f.Stat()") {
		t.Errorf("read.go must call f.Stat() on the open descriptor")
	}
	if !strings.Contains(body, "openReadFromRoot(") {
		t.Errorf("read.go must open through openReadFromRoot")
	}
}

// 11 + 12: the non-Android adapter is a documented compatibility path and must
// not reach into the safe-open API.
func TestOpenReadOtherIsCompatibilityOnly(t *testing.T) {
	src, err := os.ReadFile("open_read_other.go")
	if err != nil {
		t.Fatalf("open_read_other.go: %v", err)
	}
	body := string(src)

	if strings.Contains(body, "safefs.OpenRead") {
		t.Errorf("open_read_other.go must not call safefs.OpenRead; it is the compatibility path")
	}
	if !strings.Contains(body, "compatibility") {
		t.Errorf("open_read_other.go must document itself as a compatibility path")
	}
	if !strings.Contains(body, "Resolve") {
		t.Errorf("open_read_other.go is expected to use Root.Resolve as the compatibility path")
	}
	if !strings.Contains(body, "//go:build !android") {
		t.Errorf("open_read_other.go must carry the //go:build !android constraint")
	}
}
