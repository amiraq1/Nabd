package rtl

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

const fuzzWidth = 40

func checkLayoutInvariants(t *testing.T, in string) {
	t.Helper()
	lines, err := Layout(in, nil, fuzzWidth, Policy{Mode: ReorderAndMirror, Base: Auto})
	if err != nil {
		t.Fatalf("Layout(%q): %v", in, err)
	}

	// 1. Runes are never lost: the restored pieces (wrapping may add line
	// breaks) reproduce the input with its newline separators removed.
	restored, err := RestoreFromSource(in, clustersOf(lines))
	if err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}
	if got, want := restored, strings.ReplaceAll(in, "\n", ""); got != want {
		t.Fatalf("restore = %q, want %q", got, want)
	}

	covered := make([]bool, len(in))
	maxClusterWidth := 0
	for _, l := range lines {
		for _, c := range clustersOf([]VisualLine{l}) {
			if c.SrcBytes[0] < 0 || c.SrcBytes[1] > len(in) || c.SrcBytes[0] > c.SrcBytes[1] {
				t.Fatalf("invalid byte range %v for %q", c.SrcBytes, c.Text)
			}
			for i := c.SrcBytes[0]; i < c.SrcBytes[1]; i++ {
				if covered[i] {
					t.Fatalf("byte %d double-covered", i)
				}
				covered[i] = true
			}
			if !utf8.ValidString(c.Text) {
				t.Fatalf("cluster %q is not valid UTF-8", c.Text)
			}
			if c.Width < 0 {
				t.Fatalf("negative width for %q", c.Text)
			}
			if c.Width > maxClusterWidth {
				maxClusterWidth = c.Width
			}
			// 2. Every visual reorder step preserves the source mapping: the
			// cluster still resolves to its own bytes (or their L4 mirror).
			src, ok := SourceText(in, c)
			if !ok {
				t.Fatalf("SourceText failed for %q", c.Text)
			}
			if src != c.Text && mirrorText(src) != c.Text {
				t.Fatalf("cluster %q lost its source mapping (src %q)", c.Text, src)
			}
		}
		// 3. Width contract, except for unbreakable clusters wider than the
		// limit (documented fallback).
		if l.Width > fuzzWidth && maxClusterWidth <= fuzzWidth {
			t.Fatalf("line width %d exceeds limit %d in %q", l.Width, fuzzWidth, in)
		}
	}
	for i := range covered {
		if !covered[i] && in[i] != '\n' {
			t.Fatalf("byte %d uncovered in %q", i, in)
		}
	}
}

func FuzzLayout(f *testing.F) {
	f.Add("hello world")
	f.Add("\u0645\u0631\u062D\u0628\u0627 hello (test)!")
	f.Add("\u0627\u0628\u062C book(s)")
	f.Add("a \u2329b.1\u3009")
	f.Add("line1\nline2\n")
	f.Add("\u05D0\u05D1 internal/ui/feed.go \u05D2\u05D3")
	f.Add("\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466")
	f.Fuzz(func(t *testing.T, s string) {
		checkLayoutInvariants(t, s)
	})
}

func TestLayoutRandomProperty(t *testing.T) {
	pool := []rune("ab cdefghij 0123456789 ()[]{}<>/.:-`~!?=+ \u0645\u0631\u062D\u0628\u0627 \u0627\u0628\u062C \u05D0\u05D1\u05D2 \u064B\u0651\u064E \U0001F44D\U0001F3FD \u2764\uFE0F \u202E\u202C")
	rng := rand.New(rand.NewSource(20260929))
	for i := 0; i < 400; i++ {
		n := rng.Intn(80)
		var b strings.Builder
		for j := 0; j < n; j++ {
			b.WriteRune(pool[rng.Intn(len(pool))])
		}
		checkLayoutInvariants(t, b.String())
	}
}

func TestLayoutModesRandomProperty(t *testing.T) {
	inputs := []string{
		"\u0645\u0631\u062D\u0628\u0627 hello (test)!",
		"\u0627\u0628\u062C book(s)",
		"\u05D0\u05D1 internal/ui/feed.go \u05D2\u05D3",
	}
	for _, in := range inputs {
		for _, mode := range []Mode{Logical, Reorder, ReorderAndMirror} {
			for _, base := range []Direction{Auto, LTR, RTL} {
				lines, err := Layout(in, nil, 120, Policy{Mode: mode, Base: base})
				if err != nil {
					t.Fatalf("mode %d base %d: %v", mode, base, err)
				}
				got, err := RestoreFromSource(in, clustersOf(lines))
				if err != nil {
					t.Fatalf("mode %d base %d restore: %v", mode, base, err)
				}
				if got != in {
					t.Fatalf("mode %d base %d restore = %q", mode, base, got)
				}
			}
		}
	}
}

// TestLayoutConcurrent exercises Layout from several goroutines. The engine is
// deliberately free of shared mutable state (the tables are read-only), so the
// results must be identical to a serial run. This is the platform-neutral
// companion to `go test -race`, which the android/arm64 toolchain does not
// support.
func TestLayoutConcurrent(t *testing.T) {
	inputs := []string{
		"\u0645\u0631\u062D\u0628\u0627 \u0628\u0627\u0644\u0639\u0627\u0644\u0645 hello (test)",
		"\u05D0\u05D1 internal/ui/feed.go \u05D2\u05D3 [go] {x}",
		"\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466 \u0644\u064E\u0627 text",
	}
	type result struct {
		visual string
	}
	want := make([]result, len(inputs))
	for i, in := range inputs {
		lines, err := Layout(in, nil, 50, Policy{Mode: ReorderAndMirror, Base: Auto})
		if err != nil {
			t.Fatal(err)
		}
		want[i] = result{visual: visualOf(lines)}
	}
	errs := make(chan error, 64)
	for g := 0; g < 64; g++ {
		go func(g int) {
			in := inputs[g%len(inputs)]
			lines, err := Layout(in, nil, 50, Policy{Mode: ReorderAndMirror, Base: Auto})
			if err != nil {
				errs <- err
				return
			}
			if got := visualOf(lines); got != want[g%len(inputs)].visual {
				errs <- fmt.Errorf("goroutine %d: visual %q, want %q", g, got, want[g%len(inputs)].visual)
				return
			}
			errs <- nil
		}(g)
	}
	for g := 0; g < 64; g++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
