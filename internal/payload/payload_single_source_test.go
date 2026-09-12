package payload

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NBD-403: the fixed payload has one definition and one owner.
//
// The measurement used to live in a _test file in cmd/ag with a copy of the
// figure in internal/tools, so "the fixed payload" was three numbers that
// happened to agree — the same shape as three system prompts agreeing, which
// NBD-400 fixed for the prompt and this package fixes for the cost.
//
// The guard below enforces the property structurally: no non-test file outside
// this package may declare a fixed-payload constant. It reads the AST, so a
// comment or a string literal that merely mentions the name neither satisfies
// nor defeats it.

// protectedPrefixes are the name prefixes reserved for this package. A
// declaration matching one of them outside internal/payload is a second
// definition of a figure that must have exactly one.
var protectedPrefixes = []string{"fixedPayload", "promptOverhead", "DefaultSystemPrompt"}

// TestFixedPayloadHasASingleSourceInNonTestCode walks every non-test .go file
// in the module and fails if any declares a protected name outside this
// package. Inside this package the same scan proves the names still exist, so a
// rename cannot leave the guard vacuous.
func TestFixedPayloadHasASingleSourceInNonTestCode(t *testing.T) {
	root := moduleRoot(t)

	type decl struct {
		file string
		name string
	}
	var inside, outside []decl

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "third_party", "node_modules", "docs":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		found := protectedDeclarations(t, path)
		for _, name := range found {
			if strings.HasPrefix(filepath.ToSlash(rel), "internal/payload/") {
				inside = append(inside, decl{filepath.ToSlash(rel), name})
				continue
			}
			outside = append(outside, decl{filepath.ToSlash(rel), name})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(outside) != 0 {
		t.Fatalf("the fixed payload is declared outside internal/payload: %v. "+
			"It has one owner by design — a second definition is a second figure that happens to agree today. "+
			"Consume payload.Measure / the budget functions instead.", outside)
	}
	if len(inside) == 0 {
		t.Fatal("internal/payload declares no protected name; the guard is vacuous, so a rename has made it pass for the wrong reason")
	}
}

// protectedDeclarations returns the declared names in path that match a
// protected prefix. Only const and var declarations count: a function or a
// local variable is not a second source of the figure.
func protectedDeclarations(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	for _, d := range parsed.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, id := range vs.Names {
				for _, p := range protectedPrefixes {
					if strings.HasPrefix(id.Name, p) {
						out = append(out, id.Name)
						break
					}
				}
			}
		}
	}
	return out
}

// moduleRoot walks up from the test's directory to the directory holding
// go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for {
		if _, err := fs.Stat(os.DirFS(dir), "go.mod"); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
