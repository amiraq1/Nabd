package snap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func snapPermBits(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("snapPermBits: Stat(%q): %v", path, err)
	}
	return fi.Mode().Perm()
}

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func TestShieldStore_DirsArePrivate(t *testing.T) {
	root := t.TempDir()
	agDir := filepath.Join(root, ".ag")
	shadowDir := filepath.Join(agDir, "shadow")
	if err := shieldStore(agDir, shadowDir); err != nil {
		t.Fatalf("shieldStore: %v", err)
	}
	for _, dir := range []string{agDir, shadowDir} {
		if got := snapPermBits(t, dir); got != 0o700 {
			t.Errorf("%s mode = 0o%o, want 0o700", dir, got)
		}
	}
}

func TestShieldStore_LegacyDirsHardened(t *testing.T) {
	root := t.TempDir()
	agDir := filepath.Join(root, ".ag")
	shadowDir := filepath.Join(agDir, "shadow")
	for _, dir := range []string{agDir, shadowDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Skipf("cannot chmod: %v", err)
		}
		if snapPermBits(t, dir) != 0o755 {
			t.Skip("filesystem does not honour 0o755")
		}
	}
	if err := shieldStore(agDir, shadowDir); err != nil {
		t.Fatalf("shieldStore: %v", err)
	}
	for _, dir := range []string{agDir, shadowDir} {
		if got := snapPermBits(t, dir); got != 0o700 {
			t.Errorf("legacy %s after shield = 0o%o, want 0o700", dir, got)
		}
	}
}

func TestShieldStore_CreatesGitignore(t *testing.T) {
	root := t.TempDir()
	agDir := filepath.Join(root, ".ag")
	shadowDir := filepath.Join(agDir, "shadow")
	if err := shieldStore(agDir, shadowDir); err != nil {
		t.Fatalf("shieldStore: %v", err)
	}
	igPath := filepath.Join(agDir, ".gitignore")
	content, err := os.ReadFile(igPath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != "*" {
		t.Errorf(".gitignore = %q, want the single rule *", got)
	}
	if fi, err := os.Stat(igPath); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf(".gitignore mode = 0o%o; group/other bits must stay clear (the dir is 0700)", fi.Mode().Perm())
	}
}

func TestShieldStore_PreservesExistingGitignore(t *testing.T) {
	root := t.TempDir()
	agDir := filepath.Join(root, ".ag")
	shadowDir := filepath.Join(agDir, "shadow")
	if err := os.MkdirAll(agDir, 0o700); err != nil {
		t.Fatal(err)
	}
	userContent := "# my rule\n/logs/\n"
	igPath := filepath.Join(agDir, ".gitignore")
	if err := os.WriteFile(igPath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := shieldStore(agDir, shadowDir); err != nil {
		t.Fatalf("shieldStore: %v", err)
	}
	got, err := os.ReadFile(igPath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(got) != userContent {
		t.Errorf("existing .gitignore was modified:\n got %q\nwant %q", got, userContent)
	}
}

func TestShieldStore_Idempotent(t *testing.T) {
	root := t.TempDir()
	agDir := filepath.Join(root, ".ag")
	shadowDir := filepath.Join(agDir, "shadow")
	for i := 0; i < 3; i++ {
		if err := shieldStore(agDir, shadowDir); err != nil {
			t.Fatalf("shieldStore call %d: %v", i, err)
		}
	}
	content, err := os.ReadFile(filepath.Join(agDir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != "*" {
		t.Errorf(".gitignore after repeated shields = %q, want the single rule *", got)
	}
}

func TestShieldStore_GitStatusHidesShadow(t *testing.T) {
	if !hasGit() {
		t.Skip("git not found")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	agDir := filepath.Join(root, ".ag")
	shadowDir := filepath.Join(agDir, "shadow")
	if err := shieldStore(agDir, shadowDir); err != nil {
		t.Fatalf("shieldStore: %v", err)
	}
	blobDir := filepath.Join(shadowDir, "ab")
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blobPath := filepath.Join(blobDir, "cdef1234")
	if err := os.WriteFile(blobPath, []byte("blob content"), 0o400); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(line, "shadow") {
			t.Errorf("shadow blob visible in git status: %q", line)
		}
	}
	dryOut, err := exec.Command("git", "-C", root, "add", "-A", "--dry-run").Output()
	if err != nil {
		t.Fatalf("git add -A --dry-run: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(dryOut)), "\n") {
		if strings.Contains(line, "shadow") {
			t.Errorf("shadow staged by git add -A --dry-run: %q", line)
		}
	}
}

func TestShieldStore_GitAddForceIsLimit(t *testing.T) {
	if !hasGit() {
		t.Skip("git not found")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	agDir := filepath.Join(root, ".ag")
	shadowDir := filepath.Join(agDir, "shadow")
	if err := shieldStore(agDir, shadowDir); err != nil {
		t.Fatalf("shieldStore: %v", err)
	}
	blobDir := filepath.Join(shadowDir, "ab")
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blobPath := filepath.Join(blobDir, "cdef")
	if err := os.WriteFile(blobPath, []byte("blob"), 0o400); err != nil {
		t.Fatal(err)
	}
	// git add -f bypasses the ignore rule: documented limit, not a failure.
	out, _ := exec.Command("git", "-C", root, "add", "-f", blobPath).CombinedOutput()
	t.Logf("git add -f (documented limit): %s", out)
	exec.Command("git", "-C", root, "rm", "--cached", "-q", blobPath).Run()
}

func TestCaptureRestoreWithoutGit(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	target := filepath.Join(root, "file.txt")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := s.Capture(target)
	if err != nil {
		t.Fatalf("Capture without git: %v", err)
	}
	if st.Blob == "" {
		t.Fatal("expected a blob id")
	}
	if err := os.WriteFile(target, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(st); err != nil {
		t.Fatalf("Restore without git: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "content" {
		t.Errorf("restored %q, want %q", got, "content")
	}
}

func TestEnsureShielded_Idempotent(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := s.ensureShielded(); err != nil {
			t.Fatalf("ensureShielded call %d: %v", i, err)
		}
	}
	content, err := os.ReadFile(filepath.Join(root, ".ag", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != "*" {
		t.Errorf(".gitignore = %q, want the single rule *", got)
	}
}

// TestShieldGitignoreKeepsProjectStoreOutOfGit is the contract for the project
// store: after the first capture (the first edit the agent records), the whole
// .ag tree must be invisible to git, not just its shadow/ subdirectory.
// Ignoring only shadow/ left .ag/.gitignore itself stageable, so `git add -A`
// produced a dirty tree the user never asked for.
func TestShieldGitignoreKeepsProjectStoreOutOfGit(t *testing.T) {
	if !hasGit() {
		t.Skip("git not found")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	target := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(target, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(target); err != nil { // the first recorded edit
		t.Fatalf("Capture: %v", err)
	}

	igPath := filepath.Join(root, ".ag", ".gitignore")
	content, err := os.ReadFile(igPath)
	if err != nil {
		t.Fatalf("read %s: %v", igPath, err)
	}
	if got := strings.TrimSpace(string(content)); got != "*" {
		t.Fatalf(".gitignore content = %q, want the single rule *", got)
	}

	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(line, ".ag") {
			t.Errorf("project store visible in git status: %q\nfull output:\n%s", line, out)
		}
	}
}

// TestShieldGitignoreExistingContentStaysByteForByte: a .gitignore that
// already exists with different content is left exactly as it is — the
// write-open must not read, append to, or replace it.
func TestShieldGitignoreExistingContentStaysByteForByte(t *testing.T) {
	root := t.TempDir()
	agDir := filepath.Join(root, ".ag")
	if err := os.MkdirAll(agDir, 0o700); err != nil {
		t.Fatal(err)
	}
	igPath := filepath.Join(agDir, ".gitignore")
	custom := "# custom rules\n!keep-me\nbuild/\n"
	if err := os.WriteFile(igPath, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "file.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(target); err != nil { // one write-open
		t.Fatalf("Capture: %v", err)
	}

	got, err := os.ReadFile(igPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Fatalf(".gitignore was modified:\n got %q\nwant %q", got, custom)
	}
}

// TestShieldGitignoreNeverReplacesExistingFile: an existing .gitignore belongs
// to the user, or to an older version of this shield (which wrote /shadow/),
// and must survive byte-for-byte. Replacing it would be a write to a file this
// package does not own.
func TestShieldGitignoreNeverReplacesExistingFile(t *testing.T) {
	for name, existing := range map[string]string{
		"user content":  "# my rule\n/logs/\n",
		"legacy shield": "# nabd: prevent accidental git-staging of shadow blobs\n/shadow/\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			agDir := filepath.Join(root, ".ag")
			if err := os.MkdirAll(agDir, 0o700); err != nil {
				t.Fatal(err)
			}
			igPath := filepath.Join(agDir, ".gitignore")
			if err := os.WriteFile(igPath, []byte(existing), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "file.txt")
			if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Capture(target); err != nil {
				t.Fatalf("Capture: %v", err)
			}
			got, err := os.ReadFile(igPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != existing {
				t.Fatalf(".gitignore was modified:\n got %q\nwant %q", got, existing)
			}
		})
	}
}
