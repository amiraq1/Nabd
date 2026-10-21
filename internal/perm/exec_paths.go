// Package perm: exec_paths.go classifies a file-tool write target by what the
// write would enable later. Two tiers are protected: repository-internal state
// that a file tool has no business touching at all, and files that other
// programs execute — a build entry point, a hook, a CI workflow, an
// environment hook, or a Git attribute file that can name a filter.
package perm

import (
	"fmt"
	"path"
	"strings"
)

// WritePathTier is the classification of one write target.
type WritePathTier uint8

const (
	// WritePathOrdinary is an ordinary project file.
	WritePathOrdinary WritePathTier = iota
	// WritePathExecutionSensitive is a file whose later execution by another
	// program is the point of it: approval is required per call, and no
	// standing grant or YOLO flag may stand in for it.
	WritePathExecutionSensitive
	// WritePathInternal is repository-internal state. Writes are refused in
	// every mode rather than asked about.
	WritePathInternal
)

// execSensitiveBaseNames are files that a build tool, a package manager, a Git
// operation or a CI runner executes later. Comparison is on the base name and
// case-insensitive: a build entry point named MAKEFILE is the same entry point
// as Makefile.
var execSensitiveBaseNames = map[string]struct{}{
	"makefile":                {},
	"gnumakefile":             {},
	"justfile":                {},
	"taskfile.yml":            {},
	"taskfile.yaml":           {},
	"package.json":            {},
	".envrc":                  {},
	".npmrc":                  {},
	".pre-commit-config.yaml": {},
	".goreleaser.yaml":        {},
	".goreleaser.yml":         {},
	".gitattributes":          {},
	".gitmodules":             {},
}

// execSensitiveDirs are directories that are executed by something else, or
// that configure what runs. Presence anywhere in the path counts: a nested
// repository's workflow is still a workflow, and a nested repository's hook is
// still a hook.
var execSensitiveDirs = []string{
	"/.github/workflows/",
	"/.husky/",
	"/.vscode/",
}

// internalSegments are directories no file tool should write at all: `.git`
// holds hooks and config that execute, and `.ag` holds the session's shadow
// store and its ignore shield.
var internalSegments = map[string]struct{}{".git": {}, ".ag": {}}

// ClassifyWritePath reports the tier of one root-relative write target, and a
// reason naming what matched. The reason is part of the contract: a caller that
// refuses or asks must be able to say which path and which rule did it.
func ClassifyWritePath(rel string) (WritePathTier, string) {
	cleaned := strings.ToLower(path.Clean(strings.ReplaceAll(rel, `\`, "/")))
	if cleaned == "" || cleaned == "." {
		return WritePathOrdinary, ""
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if _, internal := internalSegments[seg]; internal {
			return WritePathInternal, fmt.Sprintf("%q is repository-internal state (%s): a file tool cannot write it", cleaned, seg)
		}
	}

	name := path.Base(cleaned)
	if _, sensitive := execSensitiveBaseNames[name]; sensitive {
		return WritePathExecutionSensitive, fmt.Sprintf("%q is an execution-sensitive file (%s): approval is required on every write", cleaned, name)
	}
	slashed := "/" + strings.TrimPrefix(cleaned, "/")
	for _, dir := range execSensitiveDirs {
		if strings.Contains(slashed, dir) {
			return WritePathExecutionSensitive, fmt.Sprintf("%q is inside an execution-sensitive directory (%s): approval is required on every write", cleaned, strings.Trim(dir, "/"))
		}
	}
	return WritePathOrdinary, ""
}
