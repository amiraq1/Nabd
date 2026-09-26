package build

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Arabic string-literal guard (Refs #223)
//
// WHY: user-facing strings in this module are written in two languages with no
// boundary between them (issue #223). Until the language ADR decides where
// Arabic may live, this guard freezes the status quo: every non-test Go source
// file in the module may carry at most the number of Arabic string literals
// recorded below, and a file that is not recorded may carry none. The count is
// a ratchet, not a whitelist: it fails when a file gains an Arabic literal AND
// when it loses one without the allowlist being lowered in the same change, so
// the inventory can only move deliberately.
//
// SCOPE: every *.go file in the module that is not *_test.go, excluding
// third_party/ (a separate module and a vendored fork). Test files are out of
// scope because they are not shipped text and several of them deliberately
// exercise Arabic strings.
//
// WHAT COUNTS: an Arabic string literal is a *ast.BasicLit of token.STRING kind
// whose unquoted value contains at least one rune in the Arabic script blocks
// U+0600–U+06FF (Arabic), U+0750–U+077F (Arabic Supplement),
// U+08A0–U+08FF (Arabic Extended-A), U+FB50–U+FDFF (Arabic Presentation
// Forms-A), or U+FE70–U+FEFF (Arabic Presentation Forms-B). Comments are NOT
// counted: the grammar-level scan (go/parser + go/ast) walks only string-literal
// nodes, so an Arabic comment or identifier is invisible to this guard.
//
// RELATIONSHIP TO THE OLDER GUARDS: cmd/ag/ascii_guard_test.go and
// internal/ui/ascii_guard_test.go stay in place. They pin a different property
// — the allowed UI symbol set over string literals in their own package — and
// are stricter there (they reject any non-ASCII rune outside the whitelist).
// This guard is module-wide, Arabic-specific, and AST-based; the three overlap
// deliberately and none replaces another. #223 closes when the allowlist is
// restricted to the ADR-0002-approved catalog files (the sanctioned display
// boundary and the explicitly-approved low-layer files), not when it empties;
// the older guards remain as the symbol whitelist.
//
// PROVENANCE: the counts were measured with this scan from master at a8a005c
// (after the #228 and #229 merges), not copied from issue #223.
// ─────────────────────────────────────────────────────────────────────────────
var arabicLiteralAllowlist = map[string]int{
	"cmd/ag/errors.go":                11,
	"internal/agent/gate.go":          4,
	"internal/config/owner_unix.go":   1,
	"internal/perm/policy.go":         1,
	"internal/provider/anthropic.go":  2,
	"internal/provider/openai.go":     1,
	"internal/registry/owner_unix.go": 1,
	"internal/snap/shadow.go":         2,
}

// arabicLiteralViolation is one reason the tree does not match the ratchet.
type arabicLiteralViolation struct {
	file   string
	reason string
}

// hasArabicRune reports whether s contains a rune from one of the Arabic
// script blocks the guard tracks.
func hasArabicRune(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0x0600 && r <= 0x06FF,
			r >= 0x0750 && r <= 0x077F,
			r >= 0x08A0 && r <= 0x08FF,
			r >= 0xFB50 && r <= 0xFDFF,
			r >= 0xFE70 && r <= 0xFEFF:
			return true
		}
	}
	return false
}

// arabicLiteralCounts walks root and returns every non-test *.go file in scope
// mapped to the number of string-literal *ast.BasicLit nodes whose unquoted
// value contains at least one Arabic rune. Files with zero Arabic literals are
// present with a zero count so an allowlist entry for a file that lost its
// Arabic, or for a file that is gone, can be told apart.
func arabicLiteralCounts(root string) (map[string]int, error) {
	counts := map[string]int{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "third_party", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		n := 0
		ast.Inspect(f, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				val = lit.Value
			}
			if hasArabicRune(val) {
				n++
			}
			return true
		})
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		counts[rel] = n
		return nil
	})
	return counts, err
}

// arabicLiteralRatchetViolations applies the ratchet rules to a set of counts:
//
//   - a file with Arabic literals that is not in the allowlist fails;
//   - a file whose count exceeds its entry fails;
//   - a file whose count is below its entry fails and asks for the entry to be
//     lowered, so the inventory cannot shrink silently;
//   - an allowlist entry with no file behind it fails.
func arabicLiteralRatchetViolations(counts map[string]int, allow map[string]int) []arabicLiteralViolation {
	var out []arabicLiteralViolation
	for file, n := range counts {
		limit, listed := allow[file]
		switch {
		case !listed && n > 0:
			out = append(out, arabicLiteralViolation{file,
				fmt.Sprintf("has %d Arabic string literal(s) and is not in the allowlist; this file must stay ASCII, or be added to the allowlist deliberately (see issue #223)", n)})
		case listed && n > limit:
			out = append(out, arabicLiteralViolation{file,
				fmt.Sprintf("has %d Arabic string literals, allowlist says %d; remove the new Arabic text, or raise the entry in the same change — that is a language decision (see issue #223)", n, limit)})
		case listed && n < limit:
			out = append(out, arabicLiteralViolation{file,
				fmt.Sprintf("has %d Arabic string literals but the allowlist still says %d; lower the entry to %d in the same change", n, limit, n)})
		}
	}
	for file := range allow {
		if _, ok := counts[file]; !ok {
			out = append(out, arabicLiteralViolation{file,
				"is in the allowlist but no such file exists; remove its entry (or restore the file)"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].file < out[j].file })
	return out
}

// TestSourceArabicLiteralRatchet applies the ratchet to the live repository tree.
func TestSourceArabicLiteralRatchet(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	counts, err := arabicLiteralCounts(root)
	if err != nil {
		t.Fatalf("scan Arabic literals: %v", err)
	}
	if len(counts) == 0 {
		t.Fatal("scan found no non-test Go files; the walker is broken, not the tree")
	}
	for _, v := range arabicLiteralRatchetViolations(counts, arabicLiteralAllowlist) {
		t.Errorf("%s: %s", v.file, v.reason)
	}
}

// TestArabicLiteralRatchetRules proves the rule table itself: each branch of
// the ratchet fails for the right reason on synthetic input, independently of
// the live tree.
func TestArabicLiteralRatchetRules(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
		cnt  map[string]int
		alw  map[string]int
		want string
	}{
		{
			name: "exact match passes",
			ok:   true,
			cnt:  map[string]int{"a.go": 2, "b.go": 0},
			alw:  map[string]int{"a.go": 2},
		},
		{
			name: "unlisted file with Arabic fails",
			cnt:  map[string]int{"a.go": 0, "b.go": 1},
			alw:  map[string]int{"a.go": 0},
			want: "b.go: has 1 Arabic string literal(s) and is not in the allowlist",
		},
		{
			name: "growth fails",
			cnt:  map[string]int{"a.go": 3},
			alw:  map[string]int{"a.go": 2},
			want: "a.go: has 3 Arabic string literals, allowlist says 2",
		},
		{
			name: "shrink fails and asks for the entry to be lowered",
			cnt:  map[string]int{"a.go": 1},
			alw:  map[string]int{"a.go": 2},
			want: "a.go: has 1 Arabic string literals but the allowlist still says 2; lower the entry to 1",
		},
		{
			name: "missing file fails",
			cnt:  map[string]int{"a.go": 0},
			alw:  map[string]int{"a.go": 0, "gone.go": 1},
			want: "gone.go: is in the allowlist but no such file exists",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := arabicLiteralRatchetViolations(tc.cnt, tc.alw)
			if tc.ok {
				if len(got) != 0 {
					t.Fatalf("expected no violations, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("expected exactly 1 violation, got %+v", got)
			}
			if full := fmt.Sprintf("%s: %s", got[0].file, got[0].reason); !strings.Contains(full, tc.want) {
				t.Fatalf("violation %q does not contain %q", full, tc.want)
			}
		})
	}
}
