package build

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// goVersionFile is the single source of the Go toolchain used by every
// workflow that builds, tests, scans, or releases nabd. go.mod's go line is
// a language minimum and is deliberately not used: a workflow reading it
// would build with an older toolchain than the one govulncheck scanned.
// A toolchain directive is forbidden too: with GOTOOLCHAIN=auto the go
// command would download a newer toolchain, and no official android/arm64
// toolchain tarball exists for Termux.
const goVersionFile = ".go-version"

var (
	releaseVersionRE   = regexp.MustCompile(`^1\.\d+\.\d+$`)
	goModGoLineRE      = regexp.MustCompile(`(?m)^go (\d+\.\d+(?:\.\d+)?)\s*$`)
	goModToolchainRE   = regexp.MustCompile(`(?m)^toolchain\s+`)
	hardcodedGoVersion = regexp.MustCompile(`^\s*go-version\s*:`)
)

func checkLatestValue(field string) (string, bool) {
	const key = "check-latest:"
	if !strings.HasPrefix(field, key) {
		return "", false
	}
	val := strings.TrimSpace(strings.TrimPrefix(field, key))
	val = strings.Trim(val, `"'`)
	return val, true
}

// checkSetupGoSteps reports every actions/setup-go step that does not read
// its toolchain from .go-version, every hardcoded go-version key, every
// check-latest that would float off that file, and the number of setup-go
// steps inspected.
func checkSetupGoSteps(name, content string) (problems []string, steps int) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if hardcodedGoVersion.MatchString(line) {
			problems = append(problems, fmt.Sprintf("%s:%d: hardcoded go-version; use go-version-file: %s", name, i+1, goVersionFile))
		}
		if !strings.Contains(line, "actions/setup-go@") {
			continue
		}
		steps++
		trimmed := strings.TrimLeft(line, " ")
		stepIndent := len(line) - len(trimmed)
		if !strings.HasPrefix(trimmed, "- ") {
			// "uses:" under "- name:": the step dash sits two columns left.
			stepIndent -= 2
		}
		found := false
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			indent := len(next) - len(strings.TrimLeft(next, " "))
			if indent <= stepIndent {
				break
			}
			field := strings.TrimSpace(next)
			if field == "go-version-file: "+goVersionFile {
				found = true
			}
			if val, ok := checkLatestValue(field); ok && val != "false" {
				problems = append(problems, fmt.Sprintf("%s:%d: setup-go sets check-latest: %s; that floats off %s", name, i+1, val, goVersionFile))
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("%s:%d: setup-go step does not use go-version-file: %s", name, i+1, goVersionFile))
		}
	}
	return problems, steps
}

func parseGoVersion(s string) ([3]int, error) {
	var v [3]int
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return v, fmt.Errorf("malformed Go version %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("malformed Go version %q", s)
		}
		v[i] = n
	}
	return v, nil
}

func lessGoVersion(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// TestGoToolchainHasSingleSource pins every setup-go step to .go-version so
// the toolchain govulncheck scans is the toolchain that builds and releases
// the binary, and checks that the pinned release satisfies go.mod.
func TestGoToolchainHasSingleSource(t *testing.T) {
	root := filepath.Join("..", "..")

	raw, err := os.ReadFile(filepath.Join(root, goVersionFile))
	if err != nil {
		t.Fatalf("read %s: %v", goVersionFile, err)
	}
	version := strings.TrimSpace(string(raw))
	if !releaseVersionRE.MatchString(version) {
		t.Fatalf("%s must hold one exact release such as 1.27.2, got %q", goVersionFile, version)
	}

	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if goModToolchainRE.Match(gomod) {
		t.Fatal("go.mod must not contain a toolchain directive; it changes source builds outside CI, including Termux")
	}
	m := goModGoLineRE.FindSubmatch(gomod)
	if m == nil {
		t.Fatal("go.mod has no go line")
	}
	minimum, err := parseGoVersion(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := parseGoVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	if lessGoVersion(pinned, minimum) {
		t.Fatalf("%s pins %s, older than the go.mod minimum %s", goVersionFile, version, m[1])
	}

	var files []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matches, err := filepath.Glob(filepath.Join(root, ".github", "workflows", pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	total := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		problems, steps := checkSetupGoSteps(filepath.Base(file), string(data))
		total += steps
		for _, p := range problems {
			t.Error(p)
		}
	}
	if total == 0 {
		t.Fatal("no actions/setup-go step found; the guard would pass vacuously")
	}
}

// TestCheckSetupGoStepsRejectsDrift proves the guard is not vacuous: each
// drift shape that reintroduces a second toolchain source is reported.
func TestCheckSetupGoStepsRejectsDrift(t *testing.T) {
	cases := []struct {
		name         string
		content      string
		wantProblems bool
	}{
		{"pinned dash step", "      - uses: actions/setup-go@sha # v7\n        with:\n          go-version-file: .go-version\n      - run: go test ./...\n", false},
		{"pinned named step", "      - name: Set up Go\n        uses: actions/setup-go@sha\n        with:\n          go-version-file: .go-version\n", false},
		{"hardcoded version", "      - uses: actions/setup-go@sha\n        with:\n          go-version: '1.27.1'\n", true},
		{"reads go.mod", "      - uses: actions/setup-go@sha\n        with:\n          go-version-file: go.mod\n", true},
		{"no with block", "      - uses: actions/setup-go@sha\n      - run: go build ./...\n", true},
		{"pin belongs to next step", "      - uses: actions/setup-go@sha\n      - name: other\n        with:\n          go-version-file: .go-version\n", true},
		{"check-latest floats", "      - uses: actions/setup-go@sha\n        with:\n          go-version-file: .go-version\n          check-latest: true\n", true},
		{"check-latest explicitly false", "      - uses: actions/setup-go@sha\n        with:\n          go-version-file: .go-version\n          check-latest: false\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems, steps := checkSetupGoSteps(tc.name, tc.content)
			if steps != 1 {
				t.Fatalf("steps = %d, want 1", steps)
			}
			if got := len(problems) > 0; got != tc.wantProblems {
				t.Fatalf("problems = %v, wantProblems = %v", problems, tc.wantProblems)
			}
		})
	}
}
