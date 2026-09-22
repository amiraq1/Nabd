package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// This file holds the structural half of the path-authority contract. These
// guards inspect the package's own source, so they parse it with go/parser and
// assert on the AST instead of matching raw text: formatting, comments, and
// variable renames can no longer break them, while a call to a banned function
// still does. A guard also fails loudly when its target stops declaring code,
// instead of passing vacuously. See docs/reports/pr139_audit.md fix #4 and the
// history recorded in docs/TECH_DEBT.md (SOURCE_INSPECTION_TESTS_FRAGILE).

// parseToolSource parses one non-test source file of this package. It fails the
// guard when the file declares no functions, because a structural check over an
// empty file would otherwise pass without inspecting anything.
func parseToolSource(t *testing.T, name string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	for _, d := range f.Decls {
		if _, ok := d.(*ast.FuncDecl); ok {
			return fset, f
		}
	}
	t.Fatalf("%s declares no functions; the structural guard would be vacuous", name)
	return nil, nil
}

// pkgCalls returns the source positions of calls of the form pkg.Sel(...).
func pkgCalls(fset *token.FileSet, f *ast.File, pkg, sel string) []string {
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		se, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || se.Sel.Name != sel {
			return true
		}
		if id, ok := se.X.(*ast.Ident); ok && id.Name == pkg {
			hits = append(hits, fset.Position(call.Pos()).String())
		}
		return true
	})
	return hits
}

// methodCalls returns the source positions of method calls named sel,
// whatever the receiver expression is.
func methodCalls(fset *token.FileSet, f *ast.File, sel string) []string {
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if se, ok := call.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == sel {
			hits = append(hits, fset.Position(call.Pos()).String())
		}
		return true
	})
	return hits
}

// identCalls returns the source positions of direct calls fn(...).
func identCalls(fset *token.FileSet, f *ast.File, fn string) []string {
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == fn {
			hits = append(hits, fset.Position(call.Pos()).String())
		}
		return true
	})
	return hits
}

// declaresFunc reports whether f declares a function with the given name.
func declaresFunc(f *ast.File, name string) bool {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
			return true
		}
	}
	return false
}

// hasComment reports whether any comment in f contains substr.
func hasComment(f *ast.File, substr string) bool {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, substr) {
				return true
			}
		}
	}
	return false
}

// hasBuildConstraint reports whether f carries the exact //go:build line for
// expr in its file header, before the package clause.
func hasBuildConstraint(f *ast.File, expr string) bool {
	want := "//go:build " + expr
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if c.Pos() < f.Package && c.Text == want {
				return true
			}
		}
	}
	return false
}

// TestReadFileUsesDescriptorStat: read.go must obtain type/size from the open
// descriptor, never from a second path lookup. os.Stat/os.Open on a path is
// exactly the resolve-then-open pattern the Android adapter removes.
func TestReadFileUsesDescriptorStat(t *testing.T) {
	fset, f := parseToolSource(t, "read.go")

	for _, banned := range []struct{ pkg, sel string }{
		{"os", "Stat"},
		{"os", "Open"},
	} {
		if hits := pkgCalls(fset, f, banned.pkg, banned.sel); len(hits) > 0 {
			t.Errorf("read.go calls %s.%s at %s; reads must go through the descriptor",
				banned.pkg, banned.sel, strings.Join(hits, ", "))
		}
	}
	if hits := methodCalls(fset, f, "Stat"); len(hits) == 0 {
		t.Error("read.go has no .Stat() call; the open descriptor is the only authority for type and size")
	}
	if hits := identCalls(fset, f, "openReadFromRoot"); len(hits) == 0 {
		t.Error("read.go never calls openReadFromRoot; the descriptor-relative open path is bypassed")
	}
}

// TestToolPathAuthorityDoesNotCallResolveDirectly: the descriptor-relative
// path adapters are authoritative in these files, and Root.Resolve is
// reporting/compatibility-only there. Only calls count, so a mention in a
// comment cannot trip the guard, and a rename in the file cannot hide one.
func TestToolPathAuthorityDoesNotCallResolveDirectly(t *testing.T) {
	for _, name := range []string{"read.go", "write.go", "write_commit.go", "grep.go"} {
		fset, f := parseToolSource(t, name)
		if hits := methodCalls(fset, f, "Resolve"); len(hits) > 0 {
			t.Errorf("%s calls .Resolve at %s; descriptor-relative path adapters are authoritative",
				name, strings.Join(hits, ", "))
		}
	}
}
