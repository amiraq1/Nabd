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
	"NoticeCategoryPermissionDenied",
	"NoticeCategoryLoopLimit",
}

func isAllowedNoticeCategory(name string) bool {
	for _, allowed := range allowedNoticeCategoryNames {
		if allowed == name {
			return true
		}
	}
	return false
}

// TestAllowedNoticeEmitsCarryStructuredPayload walks every production source in
// the module and fails when an allowed notice is constructed with raw Text and
// no Notice payload. The runtime renderer fails closed as well, but only this
// guard can see an emitter that never set the payload in the first place.
func TestAllowedNoticeEmitsCarryStructuredPayload(t *testing.T) {
	files := parseModuleSources(t)
	sites := 0
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
			if !isAllowedNoticeCategory(category) {
				return true
			}
			sites++
			if !carriesField(lit, "Notice") {
				t.Errorf("%s: %s notice is built with Text and no structured Notice payload; the model-facing line must come from NoticeData", name, category)
			}
			return true
		})
	}
	if sites < len(allowedNoticeCategoryNames) {
		t.Fatalf("found %d allowed-notice emit sites, want at least %d; the guard would otherwise pass vacuously", sites, len(allowedNoticeCategoryNames))
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
