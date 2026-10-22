package build

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Directory-exclusion parity guard
//
// WHY: the list of directory names nabd refuses to descend into exists twice:
// pathindex.DefaultExcluded (internal/pathindex/scan.go) and the unexported
// tools.skipDir (internal/tools/registry.go). Each side documents that it is
// kept in step with the other, but nothing failed when one gained or lost a
// name, so the two walkers could silently disagree — including about .ag,
// whose exclusion is a containment property: the content-addressed shadow
// history must never be offered back as a project path.
//
// SCOPE: the string literals of the case clauses inside each switch. The guard
// parses the two source files; it does not execute either filter.
//
// WHAT COUNTS: set equality in both directions, plus an anti-vacuous check
// that each function was found and lists at least one name.
// ─────────────────────────────────────────────────────────────────────────────

// caseLiteralsIn returns the string literals of the case clauses in the switch
// statement of the named function in src.
func caseLiteralsIn(t *testing.T, name, src, funcName string) []string {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}

	var out []string
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: unquote %s in %s: %v", name, lit.Value, funcName, err)
				}
				out = append(out, value)
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatalf("%s: no string case literals found in %s; the guard would pass vacuously", name, funcName)
	}
	return out
}

// caseLiterals is caseLiteralsIn for a repository-relative file.
func caseLiterals(t *testing.T, file, funcName string) []string {
	t.Helper()
	return caseLiteralsIn(t, file, readRepoFile(t, file), funcName)
}

// parityProblems returns one message per name present on only one side.
func parityProblems(pathindexNames, toolsNames []string) []string {
	inPathindex := map[string]bool{}
	for _, name := range pathindexNames {
		inPathindex[name] = true
	}
	inTools := map[string]bool{}
	for _, name := range toolsNames {
		inTools[name] = true
	}

	var problems []string
	for _, name := range pathindexNames {
		if !inTools[name] {
			problems = append(problems, "pathindex.DefaultExcluded lists "+strconv.Quote(name)+" but tools.skipDir does not")
		}
	}
	for _, name := range toolsNames {
		if !inPathindex[name] {
			problems = append(problems, "tools.skipDir lists "+strconv.Quote(name)+" but pathindex.DefaultExcluded does not")
		}
	}
	return problems
}

// TestExcludedDirsStayInStep pins the two hand-synced directory filters
// together: a name added to one list alone fails, naming the side that must
// be updated.
func TestExcludedDirsStayInStep(t *testing.T) {
	pathindexNames := caseLiterals(t, "internal/pathindex/scan.go", "DefaultExcluded")
	toolsNames := caseLiterals(t, "internal/tools/registry.go", "skipDir")

	for _, problem := range parityProblems(pathindexNames, toolsNames) {
		t.Errorf("%s; keep the two lists in step", problem)
	}
}

// TestExcludedDirsParityDetectsDrift is the negative control: a name present
// on one side only must be reported, so the parity assertion above cannot pass
// vacuously when either list silently stops contributing names.
func TestExcludedDirsParityDetectsDrift(t *testing.T) {
	const shared = `package p

func f(name string) bool {
	switch name {
	case ".git", ".ag":
		return true
	}
	return false
}
`
	const drifted = `package p

func f(name string) bool {
	switch name {
	case ".git", "node_modules":
		return true
	}
	return false
}
`

	same := parityProblems(
		caseLiteralsIn(t, "same.go", shared, "f"),
		caseLiteralsIn(t, "same.go", shared, "f"),
	)
	if len(same) != 0 {
		t.Fatalf("identical lists reported %d problems: %v", len(same), same)
	}

	problems := parityProblems(
		caseLiteralsIn(t, "one.go", shared, "f"),
		caseLiteralsIn(t, "two.go", drifted, "f"),
	)
	want := []string{
		`pathindex.DefaultExcluded lists ".ag" but tools.skipDir does not`,
		`tools.skipDir lists "node_modules" but pathindex.DefaultExcluded does not`,
	}
	if len(problems) != len(want) {
		t.Fatalf("problems = %v, want %v", problems, want)
	}
	for i := range want {
		if problems[i] != want[i] {
			t.Errorf("problems[%d] = %q, want %q", i, problems[i], want[i])
		}
	}
}
