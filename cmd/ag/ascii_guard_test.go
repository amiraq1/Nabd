package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"nabd/internal/ui"
)

// TestCmdAGStringLiteralsEnforceASCIISymbolWhitelist scans all non-test Go source files
// in cmd/ag and asserts that string literals contain no runes >= 128
// other than the explicit ui.AllowedUISymbols whitelist.
func TestCmdAGStringLiteralsEnforceASCIISymbolWhitelist(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob *.go: %v", err)
	}

	fset := token.NewFileSet()
	for _, fpath := range files {
		if strings.HasSuffix(fpath, "_test.go") {
			continue
		}

		src, err := os.ReadFile(fpath)
		if err != nil {
			t.Fatalf("read %s: %v", fpath, err)
		}

		fileNode, err := parser.ParseFile(fset, fpath, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", fpath, err)
		}

		ast.Inspect(fileNode, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}

			pos := fset.Position(lit.Pos())

			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				val = lit.Value
			}

			for _, r := range val {
				if r >= 128 && !ui.AllowedUISymbols[r] {
					t.Errorf("%s:%d: string literal %q contains unallowed rune %q (U+%04X)",
						pos.Filename, pos.Line, val, r, r)
					break
				}
			}
			return true
		})
	}
}
