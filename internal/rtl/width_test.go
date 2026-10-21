package rtl

import (
	"strings"
	"testing"
)

var widthInputs = []string{
	"\u0645\u0631\u062D\u0628\u0627 \u0628\u0627\u0644\u0639\u0627\u0644\u0645 this is a longer English sentence with internal/ui/feed.go and more words after it",
	"internal/ui/feed.go \u0645\u0631\u062D\u0628\u0627 (test) [go] {x} further trailing text",
	"short line",
	"verylongunbreakablewordthatneedsahardbreak because it has no spaces",
}

func TestWidthContracts(t *testing.T) {
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		for _, in := range widthInputs {
			lines := render(t, in, width, Policy{Mode: ReorderAndMirror, Base: Auto})
			total := 0
			for _, l := range lines {
				if l.Width > width {
					t.Fatalf("width %d: line %d exceeds limit (input %q)", width, l.Width, in)
				}
				if l.Width < 0 {
					t.Fatalf("negative width")
				}
				total += l.Width
			}
			restored, err := RestoreFromSource(in, clustersOf(lines))
			if err != nil {
				t.Fatalf("width %d: %v", width, err)
			}
			if want := strings.ReplaceAll(in, "\n", ""); restored != want {
				t.Fatalf("width %d: restore = %q, want %q", width, restored, want)
			}
			if total == 0 && in != "" {
				t.Fatalf("width %d: zero total width", width)
			}
		}
	}
}

// TestOverwideClusterIsDocumentedException: a single cluster wider than the
// limit cannot be split; the line is allowed to exceed the limit.
func TestOverwideClusterIsDocumentedException(t *testing.T) {
	lines := render(t, "\U0001F44D\U0001F3FD", 1, Policy{Mode: ReorderAndMirror, Base: Auto})
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	if lines[0].Width != 2 {
		t.Fatalf("width = %d, want 2", lines[0].Width)
	}
	if got := mustRestore(t, "\U0001F44D\U0001F3FD", lines); got != "\U0001F44D\U0001F3FD" {
		t.Fatalf("restore = %q", got)
	}
}

// TestWrapKeepsSpaces ensures wrapping never drops a rune, not even a space at
// a break point.
func TestWrapKeepsSpaces(t *testing.T) {
	in := "aa bb cc dd ee ff gg"
	for _, width := range []int{3, 4, 5, 6, 7, 8} {
		lines := render(t, in, width, Policy{Mode: ReorderAndMirror, Base: LTR})
		got, err := RestoreFromSource(in, clustersOf(lines))
		if err != nil {
			t.Fatalf("width %d: %v", width, err)
		}
		if got != in {
			t.Fatalf("width %d: wrap lost text: %q != %q", width, got, in)
		}
	}
}
