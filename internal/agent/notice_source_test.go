package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// This file holds the structural guards for the model-facing notice contract:
// an allowed notice category reaches the projection only through a structured
// NoticeData payload, and only through the renderer that bounds it. The guards
// parse production sources with go/parser and assert on the AST, so formatting,
// comments, and renames can neither fake nor break them — the same approach as
// the path-authority guards in internal/tools. Each guard first proves it found
// something to inspect, instead of passing vacuously.

// allowedNoticeCategoryNames mirrors noticeReachesModel. It is restated here on
// purpose: if the allowlist grows, this guard has to be taught the new category,
// and the omission is what forces that review.
var allowedNoticeCategoryNames = []string{
	"NoticeCategoryUndoResult",
	"NoticeCategoryLoopLimit",
}

// minExpectedAllowedNoticeSites is the minimum total number of production emit
// sites expected across the module (currently 3: 1 in NoteUndo, 2 in checkLoop).
// It is explicitly decoupled from the length of allowedNoticeCategoryNames.
// While the per-category map check prevents any category from having zero emitters,
// this aggregate floor catches the loss of one of checkLoop's two distinct emit
// sites (warning threshold vs hard limit) while the category remains non-empty.
// NOTE: This constant must be updated deliberately when intentional emitter additions
// or removals occur, so that it reflects known architecture rather than an arbitrary floor.
const minExpectedAllowedNoticeSites = 3

// inspectAllowedNoticeSites scans all parsed module sources for Event composite
// literals belonging to categories, asserts that each sets a structured Notice
// payload, and counts emit sites per category.
func inspectAllowedNoticeSites(t *testing.T, files map[string]*ast.File, categories []string) (map[string]int, []string) {
	t.Helper()
	inList := func(name string) bool {
		for _, cat := range categories {
			if cat == name {
				return true
			}
		}
		return false
	}

	counts := make(map[string]int, len(categories))
	for _, cat := range categories {
		counts[cat] = 0
	}

	for _, name := range sortedSourceNames(files) {
		ast.Inspect(files[name], func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if !isEventLiteral(lit) {
				return true
			}
			category := literalIdentValue(lit, "NoticeCategory")
			if !inList(category) {
				return true
			}
			counts[category]++
			if !carriesField(lit, "Notice") {
				t.Errorf("%s: %s notice is built with Text and no structured Notice payload; the model-facing line must come from NoticeData", name, category)
			}
			return true
		})
	}

	var missing []string
	for _, cat := range categories {
		if counts[cat] == 0 {
			missing = append(missing, cat)
		}
	}
	return counts, missing
}

// TestAllowedNoticeEmitsCarryStructuredPayload walks every production source in
// the module and fails when an allowed notice is constructed with raw Text and
// no Notice payload. It also enforces that every allowed category has at least one
// production emit site; an allowed category with zero emitters is dead schema and
// must not remain in allowedNoticeCategoryNames.
func TestAllowedNoticeEmitsCarryStructuredPayload(t *testing.T) {
	files := parseModuleSources(t)
	counts, missing := inspectAllowedNoticeSites(t, files, allowedNoticeCategoryNames)

	for _, cat := range missing {
		t.Errorf("allowed notice category %s has 0 production emit sites; every allowed category must have at least one structured emitter or be removed from the allowlist", cat)
	}

	totalSites := 0
	for _, cat := range allowedNoticeCategoryNames {
		totalSites += counts[cat]
	}
	if totalSites < minExpectedAllowedNoticeSites {
		t.Fatalf("found %d allowed-notice emit sites, want at least %d; the guard would otherwise pass vacuously", totalSites, minExpectedAllowedNoticeSites)
	}
}

// TestAllowedNoticeGuardDetectsCategoryWithoutEmitter proves that the AST guard
// refuses an allowed notice category if no production emitter exists for it.
// A guard that cannot fail on an un-emitted category cannot protect against dead schema.
func TestAllowedNoticeGuardDetectsCategoryWithoutEmitter(t *testing.T) {
	files := parseModuleSources(t)
	dummyCategories := []string{"NoticeCategorySyntheticUnemittedTarget"}
	counts, missing := inspectAllowedNoticeSites(t, files, dummyCategories)
	if counts["NoticeCategorySyntheticUnemittedTarget"] != 0 {
		t.Fatalf("synthetic category unexpectedly matched %d sites", counts["NoticeCategorySyntheticUnemittedTarget"])
	}
	if len(missing) != 1 || missing[0] != "NoticeCategorySyntheticUnemittedTarget" {
		t.Fatalf("expected guard to flag synthetic category as missing an emitter, got %v", missing)
	}
}

// TestNoticeProjectionUsesTheRenderer keeps the choke point real: the
// journal-to-wire translation must route notices through renderNotice and bound
// them with boundNoticeLine.
func TestNoticeProjectionUsesTheRenderer(t *testing.T) {
	fset, f := parseAgentSource(t, "messages.go")
	for _, fn := range []string{"renderNotice", "boundNoticeLine"} {
		if !declaresFunc(f, fn) {
			t.Fatalf("messages.go no longer declares %s; the notice choke point moved and this guard would inspect nothing", fn)
		}
	}
	if hits := identCalls(fset, f, "renderNotice"); len(hits) == 0 {
		t.Error("messages.go never calls renderNotice; an allowed notice could bypass the renderer")
	}
	if hits := identCalls(fset, f, "boundNoticeLine"); len(hits) == 0 {
		t.Error("messages.go never calls boundNoticeLine; a notice line could bypass the bound")
	}
}

// parseModuleSources parses every production .go file under the module root.
// Test files, testdata, third_party, and build output are skipped: none of them
// emit notices, and a fixture that happens to build an Event must not fail the
// guard.
func parseModuleSources(t *testing.T) map[string]*ast.File {
	t.Helper()
	root := moduleRoot(t)
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "third_party", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		out[path] = f
		return nil
	})
	if err != nil {
		t.Fatalf("scan module sources: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no production sources parsed; the guard would pass vacuously")
	}
	return out
}

// moduleRoot walks up from the package directory to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package directory; the guard cannot locate the module root")
		}
		dir = parent
	}
}

// parseAgentSource parses one non-test source file of this package.
func parseAgentSource(t *testing.T, name string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	if len(f.Decls) == 0 {
		t.Fatalf("%s declares nothing; the guard would be vacuous", name)
	}
	return fset, f
}

func sortedSourceNames(files map[string]*ast.File) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isEventLiteral(lit *ast.CompositeLit) bool {
	id, ok := lit.Type.(*ast.Ident)
	return ok && id.Name == "Event"
}

func literalField(lit *ast.CompositeLit, name string) ast.Expr {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == name {
			return kv.Value
		}
	}
	return nil
}

func literalIdentValue(lit *ast.CompositeLit, name string) string {
	id, ok := literalField(lit, name).(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

// carriesField reports whether the literal sets name to something other than nil.
func carriesField(lit *ast.CompositeLit, name string) bool {
	value := literalField(lit, name)
	if value == nil {
		return false
	}
	if id, ok := value.(*ast.Ident); ok && id.Name == "nil" {
		return false
	}
	return true
}

func declaresFunc(f *ast.File, name string) bool {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
			return true
		}
	}
	return false
}

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
