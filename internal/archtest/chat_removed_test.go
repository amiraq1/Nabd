// Package archtest holds AST-level architecture guards: invariants that are
// true of the tree rather than of any single package. They are the mechanical
// half of an ADR, so an accepted decision cannot quietly rot after the PR that
// implemented it is merged.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// retiredChatIdentifiers are the identifiers ADR-0001 retired with the Chat
// surface. None of them may reappear in any Go file, production or test.
//
// The check matches identifier nodes, not text: a comment or a string literal
// that explains the retirement neither satisfies nor defeats it, and renaming a
// symbol cannot fake compliance.
var retiredChatIdentifiers = []string{
	"NewChat", "Chat", "doChat", "chanSink", "newUISink", "uiEventBuffer",
}

// TestChatSurfaceIsRemoved is the ADR-0001 deletion guard.
//
// Chat was retired because two interactive surfaces meant every interactive
// feature had to be built twice and the model-facing prompt had more than one
// place to diverge (see newSessionLoop and
// TestSessionLoopPromptHasNoDivergentPaths). Reintroducing any identifier
// below silently reopens that risk, so the guard fails the suite instead of
// relying on review to notice.
func TestChatSurfaceIsRemoved(t *testing.T) {
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
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return perr
			}
			ast.Inspect(f, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				for _, bad := range retiredChatIdentifiers {
					if id.Name != bad {
						continue
					}
					rel, rerr := filepath.Rel(root, path)
					if rerr != nil {
						rel = path
					}
					violations = append(violations,
						rel+":"+strconv.Itoa(fset.Position(id.Pos()).Line)+": "+bad)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("ADR-0001 retired the Chat surface; these identifiers must be removed:\n%s",
			strings.Join(violations, "\n"))
	}
}

// moduleRoot resolves the repository root. This package lives at
// internal/archtest, two levels below it.
func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
