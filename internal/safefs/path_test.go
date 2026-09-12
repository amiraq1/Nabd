package safefs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRejectsEmptyPath(t *testing.T) {
	_, err := Normalize("")
	if !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("Normalize(empty) error=%v, want ErrEmptyPath", err)
	}
}

func TestNormalizeRejectsNUL(t *testing.T) {
	_, err := Normalize("safe" + string(rune(0)) + "escape")
	if !errors.Is(err, ErrNULPath) {
		t.Fatalf("Normalize(NUL) error=%v, want ErrNULPath", err)
	}
}

func TestNormalizeRejectsAbsolutePath(t *testing.T) {
	absolute := filepath.Join(
		string(filepath.Separator),
		"outside",
		"secret.txt",
	)

	_, err := Normalize(absolute)
	if !errors.Is(err, ErrAbsolutePath) {
		t.Fatalf(
			"Normalize(%q) error=%v, want ErrAbsolutePath",
			absolute,
			err,
		)
	}
}

func TestNormalizeRejectsTraversalComponents(t *testing.T) {
	// Construct adversarial inputs without filepath.Join: Join calls Clean
	// internally and would erase the explicit ".." components under test.
	sep := string(filepath.Separator)
	tests := []string{
		"..",
		".." + sep + "outside",
		"safe" + sep + "..",
		"safe" + sep + ".." + sep + "outside",
		"." + sep + ".." + sep + "outside",
	}

	for _, input := range tests {
		t.Run(strings.ReplaceAll(input, string(filepath.Separator), "_"), func(t *testing.T) {
			_, err := Normalize(input)
			if !errors.Is(err, ErrTraversal) {
				t.Fatalf(
					"Normalize(%q) error=%v, want ErrTraversal",
					input,
					err,
				)
			}
		})
	}
}

func TestNormalizeAcceptsSafeRelativePaths(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{".", "."},
		{"file.txt", "file.txt"},
		{filepath.Join("dir", "file.txt"), filepath.Join("dir", "file.txt")},
		{"." + string(filepath.Separator) + "file.txt", "file.txt"},
		{
			"dir" + string(filepath.Separator) +
				string(filepath.Separator) + "file.txt",
			filepath.Join("dir", "file.txt"),
		},
		{" name with spaces ", " name with spaces "},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := Normalize(tc.input)
			if err != nil {
				t.Fatalf("Normalize(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf(
					"Normalize(%q)=%q, want %q",
					tc.input,
					got,
					tc.want,
				)
			}
		})
	}
}

func TestNormalizeResultNeverEscapesLexically(t *testing.T) {
	inputs := []string{
		".",
		"file",
		filepath.Join("a", "b", "c"),
		"." + string(filepath.Separator) + "a",
	}

	for _, input := range inputs {
		got, err := Normalize(input)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", input, err)
		}
		if filepath.IsAbs(got) {
			t.Errorf("Normalize(%q) returned absolute path %q", input, got)
		}
		for _, component := range strings.Split(
			got,
			string(filepath.Separator),
		) {
			if component == ".." {
				t.Errorf(
					"Normalize(%q) retained traversal in %q",
					input,
					got,
				)
			}
		}
	}
}
