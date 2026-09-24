package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"nabd/internal/build"
	"nabd/internal/provider"
)

type bannerTestProvider struct {
	name string
}

func (p bannerTestProvider) Name() string { return p.name }

func (p bannerTestProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	return nil, nil
}

// TestSessionBannerFormatsProjectName verifies that sessionBanner constructs
// the banner from build.BannerPrefix(), provider name, and filepath.Base(root),
// correctly displaying the project name rather than a journal path.
func TestSessionBannerFormatsProjectName(t *testing.T) {
	prov := bannerTestProvider{name: "test-provider"}
	projectRoot := filepath.Join("home", "developer", "nabd-project")
	wantProjectName := "nabd-project"

	got := sessionBanner(prov, projectRoot)
	want := fmt.Sprintf("%s · %s · %s", build.BannerPrefix(), "test-provider", wantProjectName)

	if got != want {
		t.Fatalf("sessionBanner() = %q, want %q", got, want)
	}

	// Also verify nil provider does not panic:
	gotNil := sessionBanner(nil, projectRoot)
	wantNil := fmt.Sprintf("%s ·  · %s", build.BannerPrefix(), wantProjectName)
	if gotNil != wantNil {
		t.Fatalf("sessionBanner(nil) = %q, want %q", gotNil, wantNil)
	}
}

// TestSessionBannerHasNoDivergentCalls verifies structurally via AST that doChat,
// doChatWithFeed, and runHeadless all construct their session banner using the
// single shared sessionBanner helper rather than divergent format strings.
func TestSessionBannerHasNoDivergentCalls(t *testing.T) {
	fset := token.NewFileSet()
	files := []string{"main.go", "headless.go"}

	startCallsWithBanner := 0

	for _, fname := range files {
		file, err := parser.ParseFile(fset, fname, nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", fname, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Start" {
				return true
			}
			// We found a .Start(...) call. Verify its first argument is a call to sessionBanner.
			if len(call.Args) >= 2 {
				argCall, ok := call.Args[0].(*ast.CallExpr)
				if !ok {
					t.Fatalf("%s:%d: loop.Start called with non-call first argument: %T",
						fname, fset.Position(call.Pos()).Line, call.Args[0])
				}
				ident, ok := argCall.Fun.(*ast.Ident)
				if !ok || ident.Name != "sessionBanner" {
					t.Fatalf("%s:%d: loop.Start called with %v, want sessionBanner(...)",
						fname, fset.Position(call.Pos()).Line, argCall.Fun)
				}
				startCallsWithBanner++
			}
			return true
		})
	}

	// Must find at least 3 occurrences: doChat, doChatWithFeed, runHeadless
	if startCallsWithBanner < 3 {
		t.Fatalf("expected at least 3 sessionBanner loop.Start calls (doChat, doChatWithFeed, headless), found %d",
			startCallsWithBanner)
	}
}
