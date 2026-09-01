package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setup builds a root with a neighbour directory beside it, so that
// escapes have somewhere real to escape to.
func setup(t *testing.T) (*Root, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	outside := filepath.Join(base, "secret")
	for _, d := range []string{
		root, outside,
		filepath.Join(root, "src"),
		filepath.Join(root, "src", "deep"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "go.mod"), "module x")
	write(t, filepath.Join(root, "src", "main.go"), "package main")
	write(t, filepath.Join(outside, "passwd"), "root:x:0:0")

	r, err := NewRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return r, outside
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, name string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no symlinks")
	}
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAccepts(t *testing.T) {
	r, _ := setup(t)

	for _, in := range []string{
		"go.mod",
		"./go.mod",
		"src/main.go",
		"src/../go.mod", // climbs but lands inside
		"src/deep/../../src/main.go",
		"src",
		".",
		"new.txt",                 // does not exist yet: write target
		"src/deep/new/nested.txt", // whole tail missing
	} {
		got, err := r.Resolve(in)
		if err != nil {
			t.Errorf("Resolve(%q) refused a legitimate path: %v", in, err)
			continue
		}
		if !filepath.IsAbs(got) {
			t.Errorf("Resolve(%q) = %q, want absolute", in, got)
		}
		if !within(r.Dir(), got) {
			t.Errorf("Resolve(%q) = %q, escaped its own root", in, got)
		}
	}

	// An absolute path already inside the root is fine.
	inside := filepath.Join(r.Dir(), "src", "main.go")
	if _, err := r.Resolve(inside); err != nil {
		t.Errorf("absolute inside root refused: %v", err)
	}
}

// The list this whole package exists for.
func TestResolveRefusesTraversal(t *testing.T) {
	r, outside := setup(t)

	cases := []string{
		"..",
		"../",
		"../secret",
		"../secret/passwd",
		"../../../../../../etc/passwd",
		"src/../../secret/passwd",
		"src/deep/../../../secret/passwd",
		"./../secret/passwd",
		outside + "/passwd", // absolute, outside
		"/etc/passwd",
		"/",
		"",
		"   ",
		"a\x00/../../etc/passwd", // NUL splitting
	}
	for _, in := range cases {
		if got, err := r.Resolve(in); err == nil {
			t.Errorf("Resolve(%q) = %q, want refusal", in, got)
		}
	}
}

// A symlink is the attack that beats every string-based check, which is
// why Resolve calls EvalSymlinks before it compares anything.
func TestResolveRefusesSymlinkEscape(t *testing.T) {
	r, outside := setup(t)

	// Innocent-looking file that points at a real secret.
	link(t, filepath.Join(outside, "passwd"), filepath.Join(r.Dir(), "notes.txt"))
	if got, err := r.Resolve("notes.txt"); err == nil {
		t.Errorf("symlinked file resolved to %q, want refusal", got)
	}

	// Directory link: the escape happens mid-path, not at the end.
	link(t, outside, filepath.Join(r.Dir(), "vendor"))
	for _, in := range []string{"vendor", "vendor/passwd", "vendor/new.txt"} {
		if got, err := r.Resolve(in); err == nil {
			t.Errorf("Resolve(%q) = %q via directory symlink, want refusal", in, got)
		}
	}

	// A link that stays inside must still work: containment, not paranoia.
	link(t, filepath.Join(r.Dir(), "src", "main.go"), filepath.Join(r.Dir(), "alias.go"))
	got, err := r.Resolve("alias.go")
	if err != nil {
		t.Fatalf("internal symlink refused: %v", err)
	}
	if filepath.Base(got) != "main.go" {
		t.Errorf("Resolve(alias.go) = %q, want the real main.go", got)
	}
}

// Writing to a not-yet-existing file inside a symlinked directory is the
// case naive implementations miss: the tail is absent, so they stop
// checking, and the parent link carries the write outside.
func TestResolveRefusesEscapeViaMissingTail(t *testing.T) {
	r, outside := setup(t)
	link(t, outside, filepath.Join(r.Dir(), "out"))

	for _, in := range []string{"out/evil.txt", "out/a/b/c.txt"} {
		if got, err := r.Resolve(in); err == nil {
			t.Errorf("Resolve(%q) = %q, want refusal", in, got)
		}
	}
}

// The root may itself be reached through a symlink; that must not make
// every file under it look like an escape.
func TestRootBehindSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(real, "src", "main.go"), "package main")

	alias := filepath.Join(base, "alias")
	link(t, real, alias)

	r, err := NewRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	if r.Dir() != real {
		t.Errorf("Dir() = %q, want the resolved %q", r.Dir(), real)
	}
	if _, err := r.Resolve("src/main.go"); err != nil {
		t.Errorf("file under symlinked root refused: %v", err)
	}
}

func TestNewRootRejectsNonsense(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "file"), "x")

	if _, err := NewRoot(filepath.Join(dir, "file")); err == nil {
		t.Error("a file must not be accepted as a root")
	}
	if _, err := NewRoot(filepath.Join(dir, "nope")); err == nil {
		t.Error("a missing directory must not be accepted as a root")
	}
}

func TestRelHidesHome(t *testing.T) {
	r, _ := setup(t)
	abs := filepath.Join(r.Dir(), "src", "main.go")
	if got := r.Rel(abs); got != filepath.Join("src", "main.go") {
		t.Errorf("Rel = %q, want src/main.go", got)
	}
	if strings.Contains(r.Rel(abs), r.Dir()) {
		t.Error("Rel leaked the absolute root")
	}
}
