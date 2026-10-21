package rtl

import (
	"testing"
)

var sourceMappingInputs = []string{
	"hello world",
	"\u0645\u0631\u062D\u0628\u0627 \u0628\u0627\u0644\u0639\u0627\u0644\u0645",
	"\u0645\u064E\u0631\u0652\u062D\u064E\u0628\u064B\u0627",
	"\U0001F642",
	"\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466",
	"\u2764\uFE0F",
	"\u0627\u0641\u062A\u062D internal/ui/feed.go \u062B\u0645",
	"\u0644\u064E\u0627",
	"abc\xff\xfe\u0645\u0631\u062D\u0628\u0627", // invalid UTF-8 bytes survive as-is
}

func TestSourceMappingRoundTrip(t *testing.T) {
	for _, in := range sourceMappingInputs {
		lines := render(t, in, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
		clusters := clustersOf(lines)
		if got := mustRestore(t, in, lines); got != in {
			t.Errorf("restore %q = %q", in, got)
		}
		covered := make([]bool, len(in))
		for _, c := range clusters {
			if c.SrcBytes[0] < 0 || c.SrcBytes[1] > len(in) || c.SrcBytes[0] > c.SrcBytes[1] {
				t.Fatalf("cluster %q has invalid byte range %v", c.Text, c.SrcBytes)
			}
			for i := c.SrcBytes[0]; i < c.SrcBytes[1]; i++ {
				if covered[i] {
					t.Fatalf("byte %d covered twice in %q", i, in)
				}
				covered[i] = true
			}
			src, ok := SourceText(in, c)
			if !ok {
				t.Fatalf("SourceText failed for %q", c.Text)
			}
			// The cluster text is either the source slice or its L4 mirror.
			if src != c.Text && mirrorText(src) != c.Text {
				t.Fatalf("cluster %q does not match source %q (mirror %q)", c.Text, src, mirrorText(src))
			}
		}
		for i := range covered {
			if !covered[i] && in[i] != '\n' {
				t.Fatalf("byte %d of %q not covered", i, in)
			}
		}
	}
}

func TestVisualIsNotACopySource(t *testing.T) {
	in := "\u05D0\u05D1 internal/ui/feed.go \u05D2\u05D3"
	lines := render(t, in, 120, Policy{Mode: ReorderAndMirror, Base: RTL})
	vis := visualOf(lines)
	if vis == in {
		t.Skip("case happens to be visually identical")
	}
	if got := mustRestore(t, in, lines); got != in {
		t.Fatalf("logical copy = %q, want %q", got, in)
	}
}

func TestReorderKeepsMappingPermutation(t *testing.T) {
	in := "\u05D0\u05D1 (abc) \u05D2\u05D3 [go]"
	lines := render(t, in, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	seen := map[[2]int]int{}
	for _, c := range clustersOf(lines) {
		seen[c.SrcRunes]++
	}
	for _, c := range clusterize(in) {
		if seen[c.SrcRunes] != 1 {
			t.Fatalf("cluster %v appears %d times", c.SrcRunes, seen[c.SrcRunes])
		}
	}
	if len(seen) != len(clusterize(in)) {
		t.Fatalf("cluster count changed: %d vs %d", len(seen), len(clusterize(in)))
	}
}

func TestLogicalClustersAreSorted(t *testing.T) {
	in := "\u05D0\u05D1 cd \u05D2\u05D3"
	lines := render(t, in, 120, Policy{Mode: ReorderAndMirror, Base: RTL})
	logical := LogicalClusters(clustersOf(lines))
	for i := 1; i < len(logical); i++ {
		if logical[i-1].SrcRunes[0] > logical[i].SrcRunes[0] {
			t.Fatalf("clusters not sorted at %d", i)
		}
	}
}
