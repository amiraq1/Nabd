package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzNormalizeNeverEscapesLexically(f *testing.F) {
	sep := string(filepath.Separator)

	seeds := []string{
		"",
		".",
		"file.txt",
		"." + sep + "file.txt",
		"dir" + sep + "file.txt",
		"dir" + sep + sep + "file.txt",
		"..",
		".." + sep + "outside",
		"safe" + sep + "..",
		"safe" + sep + ".." + sep + "outside",
		"." + sep + ".." + sep + "outside",
		string(filepath.Separator) + "absolute",
		"safe" + string(rune(0)) + "outside",
		" name with spaces ",
		"اَلْعَرَبِيَّةُ" + sep + "ملف.txt",
		"emoji-👨‍👩‍👧‍👦" + sep + "file",
		"C:\\Windows\\System32",
		"\\\\server\\share\\file",
		string([]byte{0xff, 'x'}),
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		got, err := Normalize(input)
		if err != nil {
			if !errors.Is(err, ErrEmptyPath) &&
				!errors.Is(err, ErrNULPath) &&
				!errors.Is(err, ErrAbsolutePath) &&
				!errors.Is(err, ErrTraversal) {
				t.Fatalf(
					"Normalize(%q) returned an undocumented error: %v",
					input,
					err,
				)
			}
			return
		}

		if got == "" {
			t.Fatalf("Normalize(%q) succeeded with an empty result", input)
		}
		if strings.IndexByte(got, 0) >= 0 {
			t.Fatalf("Normalize(%q) retained NUL in %q", input, got)
		}
		if filepath.IsAbs(got) {
			t.Fatalf("Normalize(%q) returned absolute path %q", input, got)
		}
		if volume := filepath.VolumeName(got); volume != "" {
			t.Fatalf(
				"Normalize(%q) retained volume %q in %q",
				input,
				volume,
				got,
			)
		}
		if testHasParentComponent(got) {
			t.Fatalf(
				"Normalize(%q) retained parent traversal in %q",
				input,
				got,
			)
		}
		if cleaned := filepath.Clean(got); cleaned != got {
			t.Fatalf(
				"Normalize(%q) returned non-canonical %q; clean=%q",
				input,
				got,
				cleaned,
			)
		}

		// A normalized result must be a fixed point.
		again, err := Normalize(got)
		if err != nil {
			t.Fatalf(
				"Normalize result %q was rejected on second pass: %v",
				got,
				err,
			)
		}
		if again != got {
			t.Fatalf(
				"Normalize is not idempotent: first=%q second=%q",
				got,
				again,
			)
		}
	})
}

// testHasParentComponent is intentionally test-owned. It prevents the fuzz
// property from proving itself by calling safefs.hasParentComponent.
func testHasParentComponent(path string) bool {
	start := 0
	for i := 0; i <= len(path); i++ {
		if i != len(path) && !os.IsPathSeparator(path[i]) {
			continue
		}
		if path[start:i] == ".." {
			return true
		}
		start = i + 1
	}
	return false
}
