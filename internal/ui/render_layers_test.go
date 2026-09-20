package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// parseImports returns the sorted import paths from a parsed Go file.
func parseImports(f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// topLevelNames returns the sorted names of all top-level declarations
// (consts, vars, funcs, types) in a parsed Go file.
func topLevelNames(f *ast.File) []string {
	var out []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			out = append(out, d.Name.Name)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out = append(out, n.Name)
					}
				case *ast.TypeSpec:
					out = append(out, s.Name.Name)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// equalStrings reports whether two sorted string slices are identical.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRenderTextLayerStaysDomainFree verifies that render_text.go imports
// only the four permitted packages: fmt, strings, lipgloss, ansi. If any
// domain import (agent, presentation, etc.) appears, this test fails.
func TestRenderTextLayerStaysDomainFree(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "render_text.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing render_text.go: %v", err)
	}

	allowed := []string{
		"fmt",
		"github.com/charmbracelet/lipgloss",
		"github.com/charmbracelet/x/ansi",
		"strings",
	}

	got := parseImports(f)
	if !equalStrings(got, allowed) {
		t.Fatalf("render_text.go imports = %v\nwant exactly %v\n"+
			"The text layer must not import domain packages (agent, presentation, etc.).",
			got, allowed)
	}
}

// TestRenderTextLayerDeclarations verifies the frozen declaration inventory
// of render_text.go. If a declaration is added, removed or moved, this test
// fails and must be updated deliberately.
func TestRenderTextLayerDeclarations(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "render_text.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing render_text.go: %v", err)
	}

	// Frozen declaration set — sorted, must match topLevelNames output.
	want := []string{
		"AllowedUISymbols",
		"DefaultWidth",
		"bad",
		"block",
		"bold",
		"dim",
		"dur",
		"good",
		"green",
		"maxTailLines",
		"partialTail",
		"tail",
		"userCardStyle",
		"userMsgBg",
		"userMsgFg",
		"userRoleStyle",
		"warn",
		"wrap",
	}

	got := topLevelNames(f)

	// Filter out the blank identifier if present from var blocks.
	var filtered []string
	for _, n := range got {
		if n != "_" {
			filtered = append(filtered, n)
		}
	}
	got = filtered

	if !equalStrings(got, want) {
		// Build a readable diff.
		extra := diffStrings(got, want)
		missing := diffStrings(want, got)
		t.Fatalf("render_text.go declarations mismatch:\n  got:     %v\n  want:    %v\n  extra:   %v\n  missing: %v",
			got, want, extra, missing)
	}
}

// diffStrings returns elements in a that are not in b.
func diffStrings(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, s := range b {
		set[s] = true
	}
	var out []string
	for _, s := range a {
		if !set[s] {
			out = append(out, s)
		}
	}
	return out
}

// TestRenderEventLayerDeclarations verifies the frozen declaration inventory
// of render_event.go: the event-rendering functions that depend on agent and
// presentation.
func TestRenderEventLayerDeclarations(t *testing.T) {
	fset := token.NewFileSet()
	src := "render_event.go"
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	want := []string{
		"RenderEvent",
		"argSummary",
		"callLine",
		"flushJoin",
		"toolEnd",
	}

	got := topLevelNames(f)

	var filtered []string
	for _, n := range got {
		if n != "_" {
			filtered = append(filtered, n)
		}
	}
	got = filtered

	if !equalStrings(got, want) {
		extra := diffStrings(got, want)
		missing := diffStrings(want, got)
		t.Fatalf("%s declarations mismatch:\n  got:     %v\n  want:    %v\n  extra:   %v\n  missing: %v",
			src, got, want, extra, missing)
	}
}

// TestRenderEventLayerImportsAgent verifies that render_event.go does
// import nabd/internal/agent — the whole point of the split is that this
// layer is the only render file that touches the event domain.
func TestRenderEventLayerImportsAgent(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "render_event.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing render_event.go: %v", err)
	}

	imports := parseImports(f)
	found := false
	for _, imp := range imports {
		if imp == "nabd/internal/agent" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("render_event.go must import nabd/internal/agent; got imports: %v", imports)
	}

	// Also verify it does NOT import go/ast etc. (those belong to tests only).
	for _, imp := range imports {
		for _, banned := range []string{"go/ast", "go/parser", "go/token"} {
			if imp == banned {
				t.Errorf("render_event.go imports %q which belongs only in test files", banned)
			}
		}
	}
}

// TestNoBatchWithPrintlnInUI enforces that no file under internal/ui references
// both tea.Batch and tea.Println. Batching a print with a command that can cause
// a later print makes output order depend on scheduling.
func TestNoBatchWithPrintlnInUI(t *testing.T) {
	// chat.go is temporarily allowlisted; will be removed in the Chat-deletion PR.
	allowlist := map[string]bool{
		"chat.go": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading directory: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		node, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		hasBatch := false
		hasPrintln := false

		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != "tea" {
				return true
			}
			switch sel.Sel.Name {
			case "Batch":
				hasBatch = true
			case "Println":
				hasPrintln = true
			}
			return true
		})

		if hasBatch && hasPrintln {
			if allowlist[name] {
				continue
			}
			t.Errorf("file %q references both tea.Batch and tea.Println: batching a print with a command that can cause a later print makes output order depend on scheduling", name)
		}
	}
}
