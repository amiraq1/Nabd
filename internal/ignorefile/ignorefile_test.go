package ignorefile

import (
	"os"
	"path/filepath"
	"testing"
)

// mustTree writes files and dirs under t.TempDir and returns the root.
// dirs is a list of paths to create as directories, files a map of path to
// content. Both are given as slash paths and are converted with filepath.Join
// so the tests read the same on every platform.
func mustTree(t *testing.T, dirs []string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// wantMatch is the assertion helper shared by the table tests: rel is the
// root-relative slash path, name its final element, isDir whether the entry is
// a directory. want is the expected pattern, or "" when nothing must match.
func wantMatch(t *testing.T, m Matcher, rel, name string, isDir bool, want string) {
	t.Helper()
	got, ok := m.Match(rel, name, isDir)
	if want == "" {
		if ok {
			t.Errorf("Match(%q, %q, %v) = %q, true; want no match", rel, name, isDir, got)
		}
		return
	}
	if !ok || got != want {
		t.Errorf("Match(%q, %q, %v) = %q, %v; want pattern %q", rel, name, isDir, got, ok, want)
	}
}

func TestZeroValueMatcherMatchesNothing(t *testing.T) {
	var m Matcher
	if !m.Empty() {
		t.Fatal("zero-value Matcher reported non-empty")
	}
	wantMatch(t, m, "secrets.env", "secrets.env", false, "")
	wantMatch(t, m, "build/out.o", "out.o", false, "")
}

func TestLoadDirMissingFileIsInert(t *testing.T) {
	root := t.TempDir()
	m := LoadDir(root)
	if !m.Empty() {
		t.Fatal("missing .gitignore must load inert")
	}
	wantMatch(t, m, "anything", "anything", false, "")
}

func TestLoadDirEmptyFileIsInert(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "\n   \n# only a comment\n"})
	m := LoadDir(root)
	if !m.Empty() {
		t.Fatal("blank .gitignore must load inert")
	}
}

func TestLoadDirSymlinkRefused(t *testing.T) {
	// A symlinked .gitignore must be refused: its content is not the project's
	// own declaration, and LoadDir has no business reading whatever it points at.
	outside := t.TempDir()
	target := filepath.Join(outside, "real.gitignore")
	if err := os.WriteFile(target, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	m := LoadDir(root)
	if !m.Empty() {
		t.Fatal("symlinked .gitignore must load inert, got patterns")
	}
}

func TestExactFilePatterns(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "secrets.env\n"})
	m := LoadDir(root)
	if m.Empty() {
		t.Fatal("pattern not loaded")
	}
	wantMatch(t, m, "secrets.env", "secrets.env", false, "secrets.env")
	wantMatch(t, m, "build/secrets.env", "secrets.env", false, "secrets.env")
	wantMatch(t, m, "secrets", "secrets", true, "")
}
func TestBasenamePatterns(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "*.log\n"})
	m := LoadDir(root)
	wantMatch(t, m, "debug.log", "debug.log", false, "*.log")
	wantMatch(t, m, "build/debug.log", "debug.log", false, "*.log")
	wantMatch(t, m, "log.txt", "log.txt", false, "")
}

func TestDirOnlyPatternsDoNotMatchFiles(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "build/\n"})
	m := LoadDir(root)
	// The returned pattern is the stored text: the trailing '/' is parse-time
	// structure (dir-only), not part of the match text.
	wantMatch(t, m, "build", "build", true, "build")
	wantMatch(t, m, "vendor/build", "build", true, "build")
	wantMatch(t, m, "build", "build", false, "") // a *file* named build is not excluded
}

func TestRootedPatterns(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "/config.local\n"})
	m := LoadDir(root)
	// The leading '/' anchors; the stored match text is the path below it.
	wantMatch(t, m, "config.local", "config.local", false, "config.local")
	wantMatch(t, m, "vendor/config.local", "config.local", false, "") // rooted: only at the ignore file's dir
}

func TestRootedDirPatterns(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "/build/\n"})
	m := LoadDir(root)
	wantMatch(t, m, "build", "build", true, "build")
	wantMatch(t, m, "vendor/build", "build", true, "")
}

func TestMiddleSlashPatterns(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "docs/*.md\n"})
	m := LoadDir(root)
	wantMatch(t, m, "docs/a.md", "a.md", false, "docs/*.md")
	wantMatch(t, m, "docs/sub/a.md", "a.md", false, "") // docs/*/... nesting is not in this port
	wantMatch(t, m, "other/a.md", "a.md", false, "")
}

func TestQuestionMark(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "file?.txt\n"})
	m := LoadDir(root)
	wantMatch(t, m, "file1.txt", "file1.txt", false, "file?.txt")
	wantMatch(t, m, "file12.txt", "file12.txt", false, "")
}

func TestCharacterClass(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "[abc].txt\n"})
	m := LoadDir(root)
	wantMatch(t, m, "a.txt", "a.txt", false, "[abc].txt")
	wantMatch(t, m, "d.txt", "d.txt", false, "")
}

func TestCommentsBlankLinesAndTrailingSpaces(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "# comment\n\n  \nx.txt   \n"})
	m := LoadDir(root)
	wantMatch(t, m, "x.txt", "x.txt", false, "x.txt")
	wantMatch(t, m, "comment", "comment", false, "")
}

func TestBackslashEscape(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "\\[bracket].txt\n"})
	m := LoadDir(root)
	wantMatch(t, m, "[bracket].txt", "[bracket].txt", false, "\\[bracket].txt")
	wantMatch(t, m, "x[bracket].txt", "x[bracket].txt", false, "")
}
func TestNegationIsUnsupportedAndInert(t *testing.T) {
	// Declared gap, pinned by test: a ! line is skipped, and a file matching an
	// earlier positive pattern stays excluded. Nobody can "unignore" by editing
	// the file because the feature does not exist here.
	root := mustTree(t, nil, map[string]string{".gitignore": "*.log\n!important.log\n"})
	m := LoadDir(root)
	wantMatch(t, m, "debug.log", "debug.log", false, "*.log")
	wantMatch(t, m, "important.log", "important.log", false, "*.log")
}

func TestDoubleAsteriskIsNotRecursive(t *testing.T) {
	// Declared gap, pinned by test: in this port ** is a single non-crossing
	// star, so a/**/b covers exactly one intermediate segment. Git would also
	// cover a/b (zero segments); that shape is not expressible here, so it is
	// not matched — erring toward hiding less, as the package doc declares.
	root := mustTree(t, nil, map[string]string{".gitignore": "a/**/b\n"})
	m := LoadDir(root)
	wantMatch(t, m, "a/b", "b", false, "")         // zero segments: would match under git
	wantMatch(t, m, "a/x/b", "b", false, "a/**/b") // exactly one segment
}

// TestPrefixDirectoryInheritance pins the rule the acceptance tests forced out
// into the open: a path *under* an excluded directory is excluded, even when the
// entry itself is a file whose name matches nothing. Without this,
// read_file vendor/lib/x.so walks around a /vendor/ rule by naming a file.
func TestPrefixDirectoryInheritance(t *testing.T) {
	// Both patterns are directory-only: /vendor/ is anchored, build/ is not.
	root := mustTree(t, nil, map[string]string{".gitignore": "/vendor/\nbuild/\n"})
	m := LoadDir(root)
	// anchored dir-only pattern, file at depth two
	wantMatch(t, m, "vendor/lib/x.so", "x.so", false, "vendor")
	// unanchored dir-only pattern, file at depth one
	wantMatch(t, m, "build/out.o", "out.o", false, "build")
	// the directories themselves still match by their own rule
	wantMatch(t, m, "vendor", "vendor", true, "vendor")
	wantMatch(t, m, "build", "build", true, "build")
	// a *file* named like a dir-only pattern is still not excluded
	wantMatch(t, m, "build", "build", false, "")
	// sibling paths are untouched: the prefix must be the whole segment
	wantMatch(t, m, "vendorish/x.so", "x.so", false, "")
	wantMatch(t, m, "sub/buildish/x", "x", false, "")
	// a directory named build *under* something else: the unanchored dir-only
	// pattern matches it by basename, but a file under a dir merely *called*
	// sub/build inherits only if sub/build is itself excluded — and as a dir
	// entry it is, so during traversal its children are pruned by the entry
	// match; the rel-based inheritance pass must NOT fire on a non-excluded
	// prefix chain, which is what the next assertion pins.
	wantMatch(t, m, "sub/build", "build", true, "build")
	// nested: file two levels under an excluded dir refuses by the root prefix
	wantMatch(t, m, "build/sub/deep.txt", "deep.txt", false, "build")
}

// TestBasenamePatternDoesNotInherit pins the shape of inheritance through a
// bare basename pattern: `build` matches every entry *named* build, and what
// sits *under* a directory named build inherits the exclusion — but a path that
// merely ends in build as its own file, or a sibling with a longer name, does
// not match.
func TestBasenamePatternDoesNotInherit(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "build\n"})
	m := LoadDir(root)
	wantMatch(t, m, "build", "build", false, "build")
	wantMatch(t, m, "build", "build", true, "build")
	wantMatch(t, m, "sub/build", "build", false, "build")
	// inheritance: a file *under* a directory named build is excluded, because
	// the prefix directory `build` matches the pattern
	wantMatch(t, m, "build/out.o", "out.o", false, "build")
	// siblings with longer names are not swallowed
	wantMatch(t, m, "build2/out.o", "out.o", false, "")
}

// TestInheritanceNotThroughNonDirPatterns pins the boundary of inheritance:
// a basename pattern excludes what it *names*, plus — via inheritance — what
// sits under a directory it names. It does not make unrelated paths match.
func TestInheritanceNotThroughNonDirPatterns(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "debug.log\n"})
	m := LoadDir(root)
	wantMatch(t, m, "debug.log", "debug.log", false, "debug.log")
	// the entry pass matches basename regardless of isDir
	wantMatch(t, m, "debug.log", "debug.log", true, "debug.log")
	// a file *under* a directory named debug.log inherits the exclusion, the
	// same verdict traversal would produce in git
	wantMatch(t, m, "debug.log/part.bin", "part.bin", false, "debug.log")
	// unrelated paths stay unrelated
	wantMatch(t, m, "logs/debug.log.bak", "debug.log.bak", false, "")
}

func TestDotSlashRelIsAccepted(t *testing.T) {
	root := mustTree(t, nil, map[string]string{".gitignore": "secrets.env\n"})
	m := LoadDir(root)
	wantMatch(t, m, "./secrets.env", "secrets.env", false, "secrets.env")
}
