package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"

	"nabd/internal/agent"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderEventCoversAllKnownTypes is the source-derived replacement for the
// hand-written guard. It parses internal/agent/event.go, extracts every
// EventType constant declared there, and verifies that RenderEvent has an
// explicit case arm for each one — i.e. none of them reaches the unknown-type
// fallback ("· <type>").
//
// This test cannot go stale: adding a new EventType constant to event.go
// without a matching RenderEvent case causes a compile-and-test failure here,
// not a silent spurious "· " line on every user's screen.
func TestRenderEventCoversAllKnownTypes(t *testing.T) {
	fset := token.NewFileSet()
	//lint:ignore SA1019 ParseDir is required by the Stage A2 specification
	pkgs, err := parser.ParseDir(fset, "../agent", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing ../agent: %v", err)
	}

	var types []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					// Match: <ident> EventType = "<value>"
					tid, ok := vs.Type.(*ast.Ident)
					if !ok || tid.Name != "EventType" {
						continue
					}
					for _, v := range vs.Values {
						bl, ok := v.(*ast.BasicLit)
						if !ok {
							continue
						}
						// Strip quotes from the string literal value.
						val := strings.Trim(bl.Value, `"`)
						types = append(types, val)
					}
				}
			}
		}
	}

	if len(types) == 0 {
		t.Fatal("found zero EventType constants in ../agent — parser bug or path wrong")
	}

	for _, ty := range types {
		e := agent.Event{Type: agent.EventType(ty)}
		// Populate sub-records that nil-guard to "" to avoid a false negative.
		switch agent.EventType(ty) {
		case agent.EventRead:
			e.Read = &agent.ReadRecord{Path: "sample.go"}
		case agent.EventEdit:
			e.Edit = &agent.EditRecord{Path: "sample.go"}
		}

		got := ansi.Strip(RenderEvent(e, DefaultWidth))
		if strings.HasPrefix(got, "· ") {
			t.Errorf(
				"EventType %q fell through to the unknown-type fallback: %q\n"+
					"Add an explicit case in RenderEvent before the fallback.",
				ty, got,
			)
		}
	}
}
