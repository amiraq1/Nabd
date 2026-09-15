package build

import (
	"os"
	"strings"
	"testing"
)

// TestGitignoreIgnoresBuildOutput pins the ignore entries that keep compiled
// binaries out of the index. The repository produces binaries at the repo root
// (/ag, /nabd) and under /bin/ for local and measurement builds. Each of those
// artifacts is tens of megabytes, so a dropped entry turns a routine
// `git add -A` into a binary committed to history.
func TestGitignoreIgnoresBuildOutput(t *testing.T) {
	const path = "../../.gitignore"

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	entries := make(map[string]bool)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries[line] = true
	}

	for _, want := range []string{"/ag", "/nabd", "/bin/"} {
		if !entries[want] {
			t.Errorf("%s does not ignore build output %q", path, want)
		}
	}
}
