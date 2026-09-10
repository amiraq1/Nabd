package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"nabd/internal/payload"
	"nabd/internal/perm"
)

// TestSessionLoopPromptHasNoDivergentPaths pins the NBD-400 acceptance
// condition that Chat, Feed and headless all send the same model-facing
// contract. It checks the property structurally rather than trusting three
// literals that happen to agree:
//
//  1. newSessionLoop is the only place in non-test source that assigns a
//     System field, so no entry point can introduce a divergent prompt without
//     failing here;
//  2. the Loop it builds carries the package's single `system` value, and that
//     value still states the directives the contract depends on.
//
// The check reads the AST, not the text: a comment or string literal that
// merely mentions "System:" neither satisfies nor defeats it.
func TestSessionLoopPromptHasNoDivergentPaths(t *testing.T) {
	// (2) The constructor produces the shared prompt.
	loop := newSessionLoop(nil, nil, nil, nil)
	if loop.System != payload.DefaultSystemPrompt {
		t.Fatalf("newSessionLoop System = %q, want payload.DefaultSystemPrompt", loop.System)
	}
	for _, want := range []string{"Reply in Arabic", "50 columns", "never apologise"} {
		if !strings.Contains(loop.System, want) {
			t.Errorf("shared prompt lost %q", want)
		}
	}

	// (1) Exactly one System assignment in the package's non-test source.
	setters := systemAssignments(t)
	if len(setters) != 1 {
		t.Fatalf("System is assigned in %d places in non-test source, want exactly 1 (newSessionLoop): %v", len(setters), setters)
	}
	want := "main.go:" + strconv.Itoa(fieldLine(t, "main.go", "newSessionLoop", "System"))
	if setters[0] != want {
		t.Fatalf("the single System assignment is at %s, want the constructor's field at %s", setters[0], want)
	}
}

// systemAssignments returns "file:line" for every System field set in a
// composite literal anywhere in this package's non-test files. It is
// deliberately broader than "inside an agent.Loop literal": a wrapper type
// would otherwise be able to carry a second prompt unnoticed.
func systemAssignments(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var found []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			id, ok := kv.Key.(*ast.Ident)
			if !ok || id.Name != "System" {
				return true
			}
			pos := fset.Position(kv.Pos())
			found = append(found, filepath.Base(pos.Filename)+":"+strconv.Itoa(pos.Line))
			return true
		})
	}
	return found
}

// fieldLine reports the line of a named field inside a named function, so the
// assertion above names a position instead of hard-coding a number that drifts
// with every edit.
func fieldLine(t *testing.T, file, fn, field string) int {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	line := 0
	ast.Inspect(parsed, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok || decl.Name.Name != fn {
			return true
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == field {
				line = fset.Position(kv.Pos()).Line
				return false
			}
			return true
		})
		return false
	})
	if line == 0 {
		t.Fatalf("%s has no %s field; the shared prompt was removed", fn, field)
	}
	return line
}

// TestNewSessionLoopSharesBudgetAndGate verifies the constructor wires the
// other shared fields, so the prompt is not the only thing the entry points
// agree on: a nil gate denies every call, and a nil budget would panic.
func TestNewSessionLoopSharesBudgetAndGate(t *testing.T) {
	loop := newSessionLoop(nil, nil, gate{perm.New(nil)}, nil)
	if loop.Gate == nil {
		t.Error("constructor left the permission gate nil")
	}
	if loop.Budget == nil {
		t.Error("constructor left the budget nil")
	}
	if got := loop.MaxTurns; got != 0 {
		t.Errorf("constructor set MaxTurns=%d; the ceiling is a caller decision", got)
	}
}
