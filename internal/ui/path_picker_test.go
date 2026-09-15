package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindAtTokenRecognisesOnlyWordOpeningAt(t *testing.T) {
	cases := []struct {
		text  string
		ok    bool
		query string
		start int
	}{
		{"@", true, "", 0},
		{"@scan", true, "scan", 0},
		{"look at @internal/ui", true, "internal/ui", 8},
		{"line\n@scan", true, "scan", 5},
		{"", false, "", 0},
		{"no reference here", false, "", 0},
		{"mail me at ammar@example.com", false, "", 0},
		{"//go:build measure", false, "", 0},
		{"@scan.go and then", false, "", 0},
	}
	for _, c := range cases {
		tok, ok := findAtToken(c.text)
		if ok != c.ok {
			t.Fatalf("findAtToken(%q) ok = %v, want %v", c.text, ok, c.ok)
		}
		if !ok {
			continue
		}
		if tok.query != c.query || tok.start != c.start {
			t.Fatalf("findAtToken(%q) = {start:%d query:%q}, want {start:%d query:%q}",
				c.text, tok.start, tok.query, c.start, c.query)
		}
	}
}

func TestMatchPathsPrefersFileNamePrefix(t *testing.T) {
	paths := []string{
		"docs/scanning.md",
		"internal/pathindex/scan.go",
		"internal/scanner/other.go",
		"internal/ui/rescan_helper.go",
	}
	got := matchPaths(paths, "scan", 10)
	want := []string{
		"docs/scanning.md",           // name prefix
		"internal/pathindex/scan.go", // name prefix
		"internal/ui/rescan_helper.go", // name contains
		"internal/scanner/other.go",    // only the directory contains it
	}
	if len(got) != len(want) {
		t.Fatalf("matchPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("matchPaths = %v, want %v", got, want)
		}
	}
}

func TestMatchPathsIsCaseInsensitiveAndBounded(t *testing.T) {
	if got := matchPaths([]string{"internal/UI/Feed.go"}, "feed", 10); len(got) != 1 {
		t.Fatalf("matchPaths = %v, want the case-insensitive match", got)
	}
	many := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		many = append(many, "a/b.go")
	}
	if got := matchPaths(many, "b", 5); len(got) != 5 {
		t.Fatalf("len(matchPaths) = %d, want the limit 5", len(got))
	}
	if got := matchPaths(many, "", 7); len(got) != 7 {
		t.Fatalf("len(matchPaths) with empty query = %d, want the limit 7", len(got))
	}
}

func TestMatchPathsIsDeterministic(t *testing.T) {
	paths := []string{"b/x.go", "a/x.go", "c/x.go"}
	first := matchPaths(paths, "x", 10)
	second := matchPaths(paths, "x", 10)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("order differs between calls: %v vs %v", first, second)
		}
	}
	if first[0] != "a/x.go" {
		t.Fatalf("first = %q, want ties broken on the path", first[0])
	}
}

func TestCompleteAtTokenReplacesTheReferenceAndLeavesASpace(t *testing.T) {
	text := "please read @sca"
	tok, ok := findAtToken(text)
	if !ok {
		t.Fatal("expected a token")
	}
	got := completeAtToken(text, tok, "internal/pathindex/scan.go")
	want := "please read @internal/pathindex/scan.go "
	if got != want {
		t.Fatalf("completeAtToken = %q, want %q", got, want)
	}
	if _, still := findAtToken(got); still {
		t.Fatal("the completed text must no longer hold an open token, or the popup would reopen")
	}
}

func TestPickerSelectionWrapsAndClears(t *testing.T) {
	p := newPathPicker()
	if _, ok := p.currentPath(); ok {
		t.Fatal("a closed picker has no current path")
	}
	p.open([]string{"a.go", "b.go"}, atToken{}, false)
	p.next()
	if got, _ := p.currentPath(); got != "b.go" {
		t.Fatalf("currentPath = %q, want b.go", got)
	}
	p.next()
	if got, _ := p.currentPath(); got != "a.go" {
		t.Fatalf("currentPath = %q, want the wrap back to a.go", got)
	}
	p.prev()
	if got, _ := p.currentPath(); got != "b.go" {
		t.Fatalf("currentPath = %q, want b.go", got)
	}
	p.close()
	if p.visible || len(p.items) != 0 || p.selected != 0 {
		t.Fatal("close must reset the popup completely")
	}
}

// TestPickerDisclosesAPartialIndex is the honesty contract: a truncated scan
// must not be presented as the repository.
func TestPickerDisclosesAPartialIndex(t *testing.T) {
	p := newPathPicker()
	p.open([]string{"a.go"}, atToken{}, false)
	if strings.Contains(p.view(80), "partial") {
		t.Fatal("a complete index must not claim to be partial")
	}
	p.close()
	p.open([]string{"a.go"}, atToken{}, true)
	if !strings.Contains(p.view(80), "partial") {
		t.Fatalf("a truncated index must say so; got %q", p.view(80))
	}
}

func TestPickerRowsStayWithinTheGivenBudget(t *testing.T) {
	p := newPathPicker()
	items := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	p.open(items, atToken{}, false)
	if got := p.lineCount(); got != len(items)+2 {
		t.Fatalf("lineCount = %d, want items plus two borders (%d)", got, len(items)+2)
	}
	if got := p.lineCount(4); got != 4 {
		t.Fatalf("lineCount(4) = %d, want 4", got)
	}
	if got := p.lineCount(1); got != menuMinRows {
		t.Fatalf("lineCount(1) = %d, want the floor %d", got, menuMinRows)
	}
	for _, budget := range []int{3, 4, 5, 7, 20} {
		rows := strings.Count(p.view(80, budget), "\n") + 1
		if rows != p.lineCount(budget) {
			t.Fatalf("view emitted %d rows but lineCount promised %d at budget %d", rows, p.lineCount(budget), budget)
		}
	}
}

// TestScanPathIndexNeverOffersTheShadowStore ties the picker to the
// containment guarantee of the walk: what Scan refuses, the picker can never
// show, because the picker adds no path of its own.
func TestScanPathIndexNeverOffersTheShadowStore(t *testing.T) {
	base := t.TempDir()
	for _, dir := range []string{"src", ".ag", ".git"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, dir, "secret.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := scanPathIndex(base)
	if err != nil {
		t.Fatal(err)
	}
	if !idx.complete {
		t.Fatalf("stop = %q, want a complete scan of a three-file tree", idx.stop)
	}
	if len(idx.paths) != 1 || idx.paths[0] != "src/secret.go" {
		t.Fatalf("paths = %v, want only [src/secret.go]", idx.paths)
	}
	if got := matchPaths(idx.paths, "secret", 10); len(got) != 1 || got[0] != "src/secret.go" {
		t.Fatalf("matchPaths = %v, want only the one path the walk allowed", got)
	}
}

func TestScanPathIndexRejectsAnUnusableRoot(t *testing.T) {
	if _, err := scanPathIndex(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a root that does not exist must be an error, not an empty index presented as whole")
	}
}
