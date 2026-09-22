package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// forbiddenBuildTagRegex matches build constraints that attempt to resurrect
// !unix compatibility shims or Windows-specific code paths.
var (
	forbiddenNotUnixRegex = regexp.MustCompile(`(?m)^\s*//\s*(go:build|\+build).*!unix\b`)
)

// allowedOtherFiles lists the only non-Linux Unix fallbacks permitted:
// landlock_other.go (!linux for Darwin/BSD Landlock fallback) and
// rename_other.go (!linux && !android && !windows for Darwin atomic rename fallback).
var allowedOtherFiles = map[string]bool{
	filepath.Join("internal", "sandbox", "landlock_other.go"): true,
	filepath.Join("internal", "snap", "rename_other.go"):      true,
}

// TestUnixOnlyPlatformBoundary is the ADR-0002 architecture guard.
//
// ADR-0002 permanently committed Nabd to Unix-only (Linux, macOS, Android-Termux).
// The !unix and Windows shims were found to silently stub out critical credential
// checks (owner checks on auth.json/config, atomic fsync). This guard ensures
// no such shims, Windows source files, or build constraints ever reappear.
func TestUnixOnlyPlatformBoundary(t *testing.T) {
	root := moduleRoot(t)

	var violations []string
	for _, dir := range []string{"cmd", "internal"} {
		dirPath := filepath.Join(root, dir)
		if _, err := os.Stat(dirPath); err != nil {
			t.Fatalf("module root %s has no %s directory: %v", root, dir, err)
		}

		err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}

			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}

			// 1. Forbid any file named *_windows.go
			if strings.HasSuffix(path, "_windows.go") {
				violations = append(violations, rel+": Windows-specific file name (*_windows.go) is forbidden by ADR-0002")
			}

			// 2. Forbid any file named *_other.go unless explicitly allowlisted (Darwin/BSD fallbacks)
			if strings.HasSuffix(path, "_other.go") && !allowedOtherFiles[rel] {
				violations = append(violations, rel+": compatibility shim file (*_other.go) is forbidden by ADR-0002")
			}

			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}

			// 3. Forbid negated unix build constraints
			if forbiddenNotUnixRegex.Match(src) {
				violations = append(violations, rel+": carries forbidden negated unix build constraint")
			}

			// 4. Forbid positive 'windows' build tags (without negation)
			lines := strings.Split(string(src), "\n")
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//go:build ") || strings.HasPrefix(trimmed, "// +build ") {
					words := strings.Fields(trimmed)
					for _, w := range words {
						if w == "windows" {
							violations = append(violations, rel+":"+strconv.Itoa(i+1)+": carries forbidden positive 'windows' build tag")
						}
					}
				}
			}

			// 5. Verify the file can be parsed
			fset := token.NewFileSet()
			_, perr := parser.ParseFile(fset, path, src, 0)
			if perr != nil {
				return perr
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("ADR-0002 committed Nabd to Unix-only permanently; these platform boundary violations were found:\n%s",
			strings.Join(violations, "\n"))
	}
}
