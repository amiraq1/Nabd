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
	content, err := os.ReadFile(filepath.Join(agDir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !containsIgnoreRule(string(content), "/shadow/") {
		t.Errorf(".gitignore missing /shadow/ rule:\n%s", content)
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
	s := string(got)
	if !strings.Contains(s, userContent) {
		t.Errorf("user content stripped:\n%s", s)
	}
	if !containsIgnoreRule(s, "/shadow/") {
		t.Errorf("/shadow/ rule missing:\n%s", s)
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
	count := 0
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "/shadow/" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("/shadow/ appears %d times, want 1:\n%s", count, content)
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
	count := 0
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "/shadow/" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("/shadow/ appears %d times, want 1:\n%s", count, content)
	}
}

func TestContainsIgnoreRule(t *testing.T) {
	cases := []struct {
		text string
		rule string
		want bool
	}{
		{"/shadow/\n", "/shadow/", true},
		{"# comment\n/shadow/\n", "/shadow/", true},
		{"/shadow/   \n", "/shadow/", true},
		{"  /shadow/\n", "/shadow/", true},
		{"/shadow", "/shadow/", false},
		{"shadow/\n", "/shadow/", false},
		{"", "/shadow/", false},
	}
	for _, tc := range cases {
		got := containsIgnoreRule(tc.text, tc.rule)
		if got != tc.want {
			t.Errorf("containsIgnoreRule(%q, %q) = %v, want %v", tc.text, tc.rule, got, tc.want)
		}
	}
}
